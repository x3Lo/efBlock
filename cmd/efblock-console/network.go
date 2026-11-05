package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// NetConfig décrit la configuration réseau d'une carte : DHCP, ou adresse fixe.
type NetConfig struct {
	Iface   string
	DHCP    bool
	Address string // adresse et masque, ex "192.168.1.10/24" (adresse fixe)
	Gateway string // ex "192.168.1.1" (adresse fixe)
}

// Validate vérifie la configuration avant de l'écrire : une adresse fausse ne doit
// jamais être appliquée, car elle couperait l'accès réseau de la machine.
func (c NetConfig) Validate() error {
	if strings.TrimSpace(c.Iface) == "" {
		return errors.New("aucune carte réseau choisie")
	}
	if strings.ContainsAny(c.Iface, " \t\n=[]") {
		return fmt.Errorf("nom de carte invalide : %q", c.Iface)
	}
	if c.DHCP {
		return nil
	}
	ip, n, err := net.ParseCIDR(strings.TrimSpace(c.Address))
	if err != nil || ip.To4() == nil {
		return fmt.Errorf("adresse invalide : %q (attendu ex: 192.168.1.10/24)", c.Address)
	}
	if ones, _ := n.Mask.Size(); ones < 8 || ones > 30 {
		return fmt.Errorf("masque /%d inhabituel : choisis entre /8 et /30", ones)
	}
	gw := net.ParseIP(strings.TrimSpace(c.Gateway))
	if gw == nil || gw.To4() == nil {
		return fmt.Errorf("passerelle invalide : %q (attendu ex: 192.168.1.1)", c.Gateway)
	}
	if !n.Contains(gw) {
		return fmt.Errorf("la passerelle %s n'est pas dans le réseau %s", gw, n)
	}
	if gw.Equal(ip) {
		return errors.New("la passerelle ne peut pas être l'adresse de la machine")
	}
	return nil
}

// Render produit le fichier de configuration de systemd-networkd.
func (c NetConfig) Render() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("# Généré par la console efBlock. Modifie-le depuis le menu « Réseau ».\n")
	fmt.Fprintf(&b, "[Match]\nName=%s\n\n[Network]\n", c.Iface)
	if c.DHCP {
		b.WriteString("DHCP=ipv4\n")
	} else {
		fmt.Fprintf(&b, "Address=%s\nGateway=%s\n", strings.TrimSpace(c.Address), strings.TrimSpace(c.Gateway))
	}
	return b.String(), nil
}

// isDHCP indique si un fichier networkd généré par Render est en DHCP.
func isDHCP(unit string) bool {
	for _, l := range strings.Split(unit, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "DHCP=") {
			return true
		}
	}
	return false
}

// parseGateway lit la passerelle par défaut dans le contenu de /proc/net/route.
//
// Ce fichier liste les routes : la route par défaut a une destination à 0. Les adresses
// y sont écrites en hexadécimal, octets dans l'ordre inverse (little-endian).
func parseGateway(procRoute string) string {
	for _, l := range strings.Split(procRoute, "\n")[1:] {
		f := strings.Fields(l)
		if len(f) < 3 || f[1] != "00000000" {
			continue
		}
		v, err := strconv.ParseUint(f[2], 16, 32)
		if err != nil || v == 0 {
			continue
		}
		return net.IPv4(byte(v), byte(v>>8), byte(v>>16), byte(v>>24)).String()
	}
	return ""
}

// IfaceInfo : une carte réseau et ses adresses IPv4.
type IfaceInfo struct {
	Name  string
	Addrs []string
	Up    bool
}

// listIfaces retourne les cartes réseau utilisables (hors boucle locale).
func listIfaces() []IfaceInfo {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []IfaceInfo
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback != 0 {
			continue
		}
		info := IfaceInfo{Name: i.Name, Up: i.Flags&net.FlagUp != 0}
		if addrs, err := i.Addrs(); err == nil {
			for _, a := range addrs {
				if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
					info.Addrs = append(info.Addrs, ipn.String())
				}
			}
		}
		out = append(out, info)
	}
	return out
}

// firstIPv4 retourne la première adresse IPv4 (sans masque) d'une liste de cartes.
func firstIPv4(ifs []IfaceInfo) string {
	for _, i := range ifs {
		for _, a := range i.Addrs {
			if ip, _, err := net.ParseCIDR(a); err == nil {
				return ip.String()
			}
		}
	}
	return ""
}

// listenPort extrait le port d'une adresse d'écoute (":8080" ou "192.168.1.10:8080").
func listenPort(listen string) string {
	_, p, err := net.SplitHostPort(listen)
	if err != nil {
		return ""
	}
	return p
}

// hostOf retourne l'adresse à afficher pour une adresse d'écoute : celle indiquée si elle est
// précise, sinon l'adresse de la machine.
func hostOf(listen, fallbackIP string) string {
	h, _, err := net.SplitHostPort(listen)
	if err != nil || h == "" || h == "0.0.0.0" || h == "::" {
		return fallbackIP
	}
	return h
}

// readFile lit un fichier ; une erreur donne une chaîne vide (information facultative).
func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}
