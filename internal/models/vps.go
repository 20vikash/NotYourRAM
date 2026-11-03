package models

type VPS struct {
	Ip string
}

func CreateVPS(ip string) *VPS {
	return &VPS{Ip: ip}
}
