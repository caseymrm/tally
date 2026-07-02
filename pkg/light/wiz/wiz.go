// Package wiz controls Philips Wiz smart bulbs over their local UDP
// protocol (JSON on port 38899, no auth, no hub). Importing this package
// registers the "wiz" provider with pkg/light.
package wiz

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/caseymrm/tally/pkg/light"
)

const (
	port           = 38899
	requestTimeout = time.Second
	requestRetries = 3
	discoveryWait  = 2 * time.Second
)

func init() {
	light.Register(provider{})
}

type provider struct{}

func (provider) Name() string { return "wiz" }

func (provider) Connect(cfg light.Config) (light.Light, error) {
	if cfg.ID == "" {
		return nil, fmt.Errorf("wiz: config has no MAC address")
	}
	return &Bulb{mac: cfg.ID, name: cfg.Name, addr: cfg.Address}, nil
}

func (provider) Discover(ctx context.Context) ([]light.Config, error) {
	return Discover(ctx)
}

// Bulb is one Wiz bulb, identified by MAC. If the bulb stops answering at
// its last known address (DHCP gave it a new lease), SetStatus re-discovers
// it by MAC and retries.
type Bulb struct {
	mu   sync.Mutex
	mac  string
	name string
	addr string
}

// params is the wire format for setPilot.
type params struct {
	State   bool `json:"state"`
	R       *int `json:"r,omitempty"`
	G       *int `json:"g,omitempty"`
	B       *int `json:"b,omitempty"`
	Dimming *int `json:"dimming,omitempty"`
}

type request struct {
	Method string  `json:"method"`
	Params *params `json:"params,omitempty"`
}

type response struct {
	Method string `json:"method"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Result struct {
		Success    bool   `json:"success"`
		Mac        string `json:"mac"`
		ModuleName string `json:"moduleName"`
	} `json:"result"`
}

func intp(v int) *int { return &v }

func pilotFor(status light.Status) *params {
	if status == light.Busy {
		return &params{State: true, R: intp(255), G: intp(0), B: intp(0), Dimming: intp(100)}
	}
	return &params{State: false}
}

// SetStatus turns the bulb red (Busy) or off (Free).
func (b *Bulb) SetStatus(status light.Status) error {
	b.mu.Lock()
	addr := b.addr
	b.mu.Unlock()

	req := request{Method: "setPilot", Params: pilotFor(status)}
	if addr != "" {
		if err := send(addr, req); err == nil {
			return nil
		}
	}
	// The bulb may have moved to a new IP; find it again by MAC.
	ctx, cancel := context.WithTimeout(context.Background(), discoveryWait+time.Second)
	defer cancel()
	cfgs, err := Discover(ctx)
	if err != nil {
		return fmt.Errorf("wiz: bulb %s unreachable at %q and re-discovery failed: %w", b.mac, addr, err)
	}
	for _, cfg := range cfgs {
		if cfg.ID == b.mac {
			b.mu.Lock()
			b.addr = cfg.Address
			b.mu.Unlock()
			return send(cfg.Address, req)
		}
	}
	return fmt.Errorf("wiz: bulb %s not found on the network", b.mac)
}

// Config returns the bulb's config with its current address.
func (b *Bulb) Config() light.Config {
	b.mu.Lock()
	defer b.mu.Unlock()
	return light.Config{Provider: "wiz", ID: b.mac, Name: b.name, Address: b.addr}
}

// send performs one UDP request/response exchange with retries.
func send(addr string, req request) error {
	payload, err := json.Marshal(req)
	if err != nil {
		return err
	}
	raddr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", addr, port))
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < requestRetries; attempt++ {
		lastErr = func() error {
			conn, err := net.DialUDP("udp4", nil, raddr)
			if err != nil {
				return err
			}
			defer conn.Close()
			if _, err := conn.Write(payload); err != nil {
				return err
			}
			conn.SetReadDeadline(time.Now().Add(requestTimeout))
			buf := make([]byte, 4096)
			n, err := conn.Read(buf)
			if err != nil {
				return err
			}
			var resp response
			if err := json.Unmarshal(buf[:n], &resp); err != nil {
				return err
			}
			if resp.Error != nil {
				return fmt.Errorf("wiz: bulb error %d: %s", resp.Error.Code, resp.Error.Message)
			}
			return nil
		}()
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

// Discover finds Wiz bulbs on the local network by broadcasting a
// registration probe (the same handshake the Wiz app uses) and asking each
// responder for its system config to build a friendly name.
func Discover(ctx context.Context) ([]light.Config, error) {
	// The socket needs SO_BROADCAST to send the probe, which Go doesn't
	// set by default.
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var sockErr error
			if err := c.Control(func(fd uintptr) {
				sockErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
			}); err != nil {
				return err
			}
			return sockErr
		},
	}
	pc, err := lc.ListenPacket(ctx, "udp4", ":0")
	if err != nil {
		return nil, err
	}
	conn := pc.(*net.UDPConn)
	defer conn.Close()

	probe := []byte(`{"method":"registration","params":{"phoneMac":"AAAAAAAAAAAA","register":false,"phoneIp":"1.2.3.4","id":"1"}}`)
	targets := broadcastAddrs()

	deadline := time.Now().Add(discoveryWait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetReadDeadline(deadline)

	// UDP broadcast is lossy; probe a few times over the window.
	go func() {
		for i := 0; i < 3; i++ {
			for _, target := range targets {
				conn.WriteToUDP(probe, &net.UDPAddr{IP: target, Port: port})
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	}()

	found := map[string]string{} // mac -> ip
	buf := make([]byte, 4096)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			break // deadline reached
		}
		var resp response
		if err := json.Unmarshal(buf[:n], &resp); err != nil {
			continue
		}
		if resp.Result.Mac != "" {
			found[strings.ToLower(resp.Result.Mac)] = from.IP.String()
		}
	}

	var mu sync.Mutex
	var configs []light.Config
	var wg sync.WaitGroup
	for mac, ip := range found {
		wg.Add(1)
		go func(mac, ip string) {
			defer wg.Done()
			cfg := light.Config{
				Provider: "wiz",
				ID:       mac,
				Name:     friendlyName("", mac),
				Address:  ip,
			}
			if module, err := systemConfig(ip); err == nil {
				cfg.Name = friendlyName(module, mac)
			}
			mu.Lock()
			configs = append(configs, cfg)
			mu.Unlock()
		}(mac, ip)
	}
	wg.Wait()
	return configs, nil
}

// systemConfig asks one bulb for its module name.
func systemConfig(addr string) (string, error) {
	raddr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", addr, port))
	if err != nil {
		return "", err
	}
	conn, err := net.DialUDP("udp4", nil, raddr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(`{"method":"getSystemConfig","params":{}}`)); err != nil {
		return "", err
	}
	conn.SetReadDeadline(time.Now().Add(requestTimeout))
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return "", err
	}
	var resp response
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		return "", err
	}
	return resp.Result.ModuleName, nil
}

// friendlyName builds a display name from a Wiz module name like
// "ESP01_SHRGB1C_31" plus the MAC's tail to tell twins apart.
func friendlyName(module, mac string) string {
	kind := "light"
	switch {
	case strings.Contains(module, "RGB"):
		kind = "color bulb"
	case strings.Contains(module, "TW"):
		kind = "tunable white bulb"
	case strings.Contains(module, "DW"):
		kind = "dimmable bulb"
	case strings.Contains(module, "SOCKET"):
		kind = "smart plug"
	}
	suffix := mac
	if len(mac) > 6 {
		suffix = mac[len(mac)-6:]
	}
	return fmt.Sprintf("Wiz %s %s", kind, strings.ToUpper(suffix))
}

// broadcastAddrs returns the IPv4 broadcast address of every up interface,
// plus the limited broadcast address.
func broadcastAddrs() []net.IP {
	addrs := []net.IP{net.IPv4bcast}
	ifaces, err := net.Interfaces()
	if err != nil {
		return addrs
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagBroadcast == 0 {
			continue
		}
		ifaceAddrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range ifaceAddrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil {
				continue
			}
			bcast := make(net.IP, 4)
			for i := range bcast {
				bcast[i] = ip[i] | ^ipnet.Mask[i]
			}
			addrs = append(addrs, bcast)
		}
	}
	return addrs
}
