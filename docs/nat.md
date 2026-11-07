# NAT and Return-Path Routing in NotYourRAM (NYR)

## Goal

NYR exposes customer services through allocated ports on a shared public IPv4 address. The customer's service runs behind a WireGuard tunnel, potentially on a home connection without a public IPv4 address.

The intended data plane uses **stateless, deterministic address/port translation** rather than Linux conntrack-based NAT. Translation rules are configured per published service, not created per connection. Both the incoming packet and its reply must traverse the NYR gateway.

> **Design status:** This document describes the proposed architecture, not an already implemented data plane.

## Example topology

| Component | Address |
| --- | --- |
| Internet client | `198.51.100.5:50000` |
| NYR public interface (`eth0`) | `203.0.113.10` |
| NYR WireGuard interface (`wg0`) | `10.80.0.1` |
| Customer WireGuard address | `10.80.0.2` |
| Allocated public endpoint | `203.0.113.10:20022/tcp` |
| Customer service | `10.80.0.2:22/tcp` |

The addresses above are illustrative.

The configured service mapping is:

```text
TCP 203.0.113.10:20022 <-> 10.80.0.2:22
```

## Forward path: Internet → customer

The Internet client initiates a connection:

```text
198.51.100.5:50000 -> 203.0.113.10:20022
```

At NYR's public ingress, the stateless translator looks up the destination protocol, public IP, and port, then rewrites the **destination**:

```text
Before: 198.51.100.5:50000 -> 203.0.113.10:20022
After:  198.51.100.5:50000 -> 10.80.0.2:22
```

The client's source IP and port remain unchanged. Linux routes the translated packet toward `wg0`, and WireGuard delivers it to the customer.

```text
Internet client
     |
     | TCP SYN to 203.0.113.10:20022
     v
NYR eth0
     |
     | Stateless destination translation
     | 203.0.113.10:20022 -> 10.80.0.2:22
     v
NYR routing -> wg0 -> WireGuard tunnel
     |
     v
Customer 10.80.0.2:22
```

## The return-path problem

The customer replies with:

```text
10.80.0.2:22 -> 198.51.100.5:50000
```

A normal destination-based route lookup on the customer's machine might find:

```text
10.80.0.0/24 dev wg0
default via 192.168.1.1 dev eth0
```

The reply destination (`198.51.100.5`) is **not** in the WireGuard subnet, so the ordinary default route points to the customer's home ISP. The reply bypasses NYR, and NYR cannot reverse the translation. The client expects a response from `203.0.113.10:20022`, not from the customer's home network.

**The reply must return through NYR.** This is true for both conntrack-based NAT and stateless NAT.

## Solution: source-based policy routing on the customer

Linux policy-based routing (PBR) can select a routing table using the **source address**, not only the destination address.

Because the customer service receives packets addressed to `10.80.0.2`, its replies normally use `10.80.0.2` as their source. We can route packets from that source through the WireGuard tunnel while leaving the machine's ordinary Internet traffic alone.

### Example Linux configuration

Run on the **customer** machine:

```bash
# Dedicated table for traffic sourced from the customer's WG address.
sudo ip route add default dev wg0 table 100
sudo ip rule add priority 100 from 10.80.0.2/32 lookup 100

# Inspect the policy and routing decision.
ip rule show
ip route show table 100
ip route get 198.51.100.5 from 10.80.0.2
```

The rule means:

```text
Source = 10.80.0.2 -> routing table 100 -> wg0
Other sources      -> ordinary routing rules/table
```

**WireGuard peer selection is separate from Linux routing.** On the customer, the NYR peer must allow arbitrary IPv4 destinations because replies are addressed to Internet clients:

```ini
[Interface]
Address = 10.80.0.2/32
PrivateKey = <CUSTOMER_PRIVATE_KEY>
Table = off

[Peer]
PublicKey = <NYR_PUBLIC_KEY>
Endpoint = [<NYR_PUBLIC_IPV6>]:51820
AllowedIPs = 0.0.0.0/0
PersistentKeepalive = 25
```

`Table = off` is a `wg-quick` setting that prevents it from automatically installing routes from `AllowedIPs`. NYR instead installs the explicit source-based policy route shown above. `AllowedIPs = 0.0.0.0/0` allows the NYR peer to carry packets destined for arbitrary IPv4 clients; **it does not by itself redirect the machine's entire Internet traffic** when automatic route installation is disabled.

The WireGuard transport endpoint in this example is IPv6, so the outer tunnel packets use the host's ordinary IPv6 routing rather than the IPv4 source-based rule.

### Return path with PBR

```text
Customer 10.80.0.2:22
     |
     | Reply: 10.80.0.2:22 -> 198.51.100.5:50000
     v
ip rule: source 10.80.0.2 -> table 100
     |
     v
wg0 -> WireGuard tunnel -> NYR
     |
     | Stateless source translation
     | 10.80.0.2:22 -> 203.0.113.10:20022
     v
NYR eth0
     |
     v
Internet client 198.51.100.5:50000
```

The Internet client sees the expected public source endpoint. The customer's application still sees the real Internet client's IP address.

## Stateless forward and reverse translation

NYR can maintain **service mappings**, rather than per-connection NAT state.

Conceptually:

```text
Forward map:
  (protocol, public IP, public port)
    -> (customer WG IP, private port)

Reverse map:
  (protocol, customer WG IP, private port)
    -> (public IP, public port)
```

For the example:

```text
Forward: (TCP, 203.0.113.10, 20022) -> (10.80.0.2, 22)
Reverse: (TCP, 10.80.0.2, 22)       -> (203.0.113.10, 20022)
```

These mappings are independent of how many clients connect to the service. They can be implemented with eBPF maps and packet rewriting at appropriate TC hooks. The translator must update IP/transport checksums and handle relevant packet edge cases.

### Important constraint: reverse mapping must be unique

Consider two public endpoints pointing to the same private endpoint:

```text
203.0.113.10:20022 -> 10.80.0.2:22
203.0.113.10:20023 -> 10.80.0.2:22
```

A reply from `10.80.0.2:22` contains no information identifying which public port the client originally used. Without per-flow state or some other distinguishing information, NYR cannot choose the correct public source port.

**For purely stateless reverse translation, each `(protocol, customer WG IP, private port)` must resolve to exactly one public endpoint.** Alternatively, NYR must design a reversible mapping that preserves the necessary information.

## eBPF and conntrack

The proposed packet path is:

```text
Public NIC ingress -> stateless destination rewrite -> route -> wg0
wg0 ingress        -> stateless source rewrite      -> route -> public NIC
```

TC ingress is a possible attachment point for the two translation programs. The customer-to-NYR packet is decrypted before it appears as an inner IP packet on NYR's `wg0` interface.

Linux conntrack-based NAT is **not required** for deterministic translation if NYR performs both directions itself. To actually avoid allocating conntrack entries for these forwarded packets, NYR must also arrange for the relevant traffic to bypass Netfilter tracking (for example, carefully scoped `NOTRACK` rules at raw priority), or use a packet path that bypasses it. Implementing eBPF rewriting alone does **not** automatically disable conntrack.

Do not indiscriminately apply `NOTRACK` to the entire public interface: NYR may still have other traffic that needs stateful filtering or NAT. The bypass must match the intended data plane in both directions.

## Operational requirements

- **IP forwarding:** NYR must enable IPv4 forwarding and permit the intended forwarding path.
- **Routing:** NYR must have a route to each customer WireGuard address, and the customer's source-based route must send service replies back through NYR.
- **WireGuard peer rules:** The customer's NYR peer needs `AllowedIPs = 0.0.0.0/0` for arbitrary Internet-client destinations. NYR's customer peer should restrict allowed tunnel source addresses to that customer's assigned address(es).
- **Reverse-path filtering:** Linux `rp_filter` can reject asymmetric-looking traffic; verify its behavior on both ends with the chosen policy routes.
- **Service binding:** The customer application must reply from its WireGuard address (for example, by binding the service to `10.80.0.2`).
- **Translation correctness:** Handle checksums, fragmentation, ICMP errors/PMTU, and IPv4 packet validation. TCP and UDP share the basic routing requirement but have different packet-processing details.
- **Abuse controls:** Removing conntrack from the forwarding path eliminates conntrack-table exhaustion **for that path**, not packet-rate, bandwidth, CPU, or application-layer exhaustion. Apply tenant isolation and resource controls separately.
- **Policy scope:** A source-only rule routes *all* traffic originating from `10.80.0.2` through NYR, including customer-initiated traffic using that source. If NYR must allow only replies to published services, add a more selective policy/marking design.

## Summary

1. NYR rewrites the destination of incoming traffic from its public allocated port to the customer's WireGuard service address.
2. The customer's reply is addressed to the original Internet client, so ordinary destination-based routing would send it through the home ISP.
3. **Source-based policy routing** directs replies originating from the customer's WireGuard IP back into the WireGuard tunnel.
4. NYR performs deterministic reverse source translation and forwards the reply to the Internet client.
5. With unique reversible service mappings, this can operate **without per-connection NAT state**, provided conntrack is bypassed for the intended forwarding path.

The key insight: **routing can depend on source IP, not just destination IP.** That makes symmetric return routing possible without changing the customer's normal default route.