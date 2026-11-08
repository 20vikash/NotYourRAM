# NYR — Traffic Control Design

**Status:** Proposed

## Objective

Enforce per-customer bandwidth allocations on shared NYR gateways without conntrack. NYR is a port-mapping service, **not** a general-purpose VPN.

## Architecture

- **Scheduler:** HTB with one class per customer and `fq_codel` leaf qdiscs.
- **Interfaces:** Independent HTB trees on `eth0` egress and `wg0` egress.
- **Forwarding:** NYR's stateless translation/forwarding data plane remains separate from traffic control.

```text
Internet <---> eth0 [HTB egress] <---> NYR forwarding <---> wg0 [HTB egress] <---> Customer
               Customer -> Internet                     Internet -> Customer

Each HTB tree:
  root HTB
    └── aggregate class (budget B)
          ├── customer A class (rate R, ceil C) -> fq_codel
          ├── customer B class (rate R, ceil C) -> fq_codel
          └── ...
```

## Bandwidth policy

Inputs:

- `A`: VPS advertised bandwidth (Mbps)
- `N`: planned customer capacity per VPS (fixed for the capacity plan)
- `u`: utilization factor, initially `0.90`

```text
B    = A * u
rate = B / N
ceil = min(2 * rate, B)
```

`rate` is the configured per-customer baseline; `ceil` permits borrowing unused capacity up to 2× the baseline. Both are scheduling limits, not end-to-end throughput guarantees.

**Example:** `A = 1000 Mbps`, `N = 20` → `B = 900 Mbps`, `rate = 45 Mbps`, `ceil = 90 Mbps` per customer.

Use **planned capacity**, not currently online customers, to avoid changing existing allocations on every signup. Reserve additional headroom if gateway/control traffic shares the shaped parent. Calibrate `u` using measured WireGuard and public-interface throughput.

## Classification by direction

### Internet → customer (`wg0` egress)

1. NYR translates the published endpoint to the customer's tunnel address.
2. `tc flower` matches the inner packet's **destination WireGuard IP**.
3. The packet enters that customer's HTB class and `fq_codel` leaf.

```text
eth0 ingress -> destination translation -> wg0 egress
                                         dst 10.80.0.2 -> class 1:10
```

### Customer → Internet (`eth0` egress)

1. On **`wg0` ingress**, identify the customer and validate the permitted source/tunnel identity.
2. The eBPF TC program sets a customer-specific **`skb->mark`** on the inner packet.
3. NYR performs return-path source translation.
4. On **`eth0` egress**, a `tc fw` filter maps the mark to the customer's HTB class.

```text
wg0 ingress -> skb->mark = 0x10 -> source translation -> eth0 egress
                                                      fwmark 0x10 -> class 1:10
```

The mark is **packet metadata (`struct sk_buff`)**, not TCP metadata or a conntrack mark. The marking hook must run on the forwarded inner packet; verify mark preservation through the actual translation path. Reserve a mark mask/namespace to avoid collisions with routing or firewall policies.

## Example `tc` setup

Illustrative two-customer configuration (`10.80.0.2` / `10.80.0.3`, marks `0x10` / `0x20`). Assumes no conflicting root qdiscs and that the eBPF marking and forwarding rules are installed separately.

```bash
for DEV in eth0 wg0; do
  tc qdisc add dev "$DEV" root handle 1: htb
  tc class add dev "$DEV" parent 1: classid 1:1 htb rate 900mbit ceil 900mbit

  tc class add dev "$DEV" parent 1:1 classid 1:10 htb rate 45mbit ceil 90mbit
  tc class add dev "$DEV" parent 1:1 classid 1:20 htb rate 45mbit ceil 90mbit

  tc qdisc add dev "$DEV" parent 1:10 handle 10: fq_codel
  tc qdisc add dev "$DEV" parent 1:20 handle 20: fq_codel
done

# Internet -> customer: classify by translated destination.
tc filter add dev wg0 parent 1: protocol ip flower dst_ip 10.80.0.2 classid 1:10
tc filter add dev wg0 parent 1: protocol ip flower dst_ip 10.80.0.3 classid 1:20

# Customer -> Internet: classify by skb->mark assigned at wg0 ingress.
tc filter add dev eth0 parent 1: protocol ip handle 0x10 fw classid 1:10
tc filter add dev eth0 parent 1: protocol ip handle 0x20 fw classid 1:20
```

Production provisioning must add classes/filters for all customers, define a deliberate policy for unmatched packets, and manage existing root qdiscs. The sample does not install the eBPF marking program.

## Forwarding and limits

- Permit published inbound mappings and the corresponding configured return-path translation; deny arbitrary customer internet egress.
- Each HTB tree enforces **independent directional limits**. `90 Mbps` in each direction is not a combined `90 Mbps` limit.
- `wg0` shapes inner packets before WireGuard encapsulation; outer UDP/IP overhead is additional.
