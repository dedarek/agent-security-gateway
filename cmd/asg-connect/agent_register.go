package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/user"
	"runtime"
	"strings"
	"time"
)

type agentRegistration struct {
	SessionID   string   `json:"session_id"`
	AgentID     string   `json:"agent_id"`
	ProbeID     string   `json:"probe_id"`
	MachineID   string   `json:"machine_id"`
	MachineName string   `json:"machine_name"`
	Alias       string   `json:"alias"`
	AgentType   string   `json:"agent_type"`
	ProcessID   int      `json:"process_id"`
	OS          string   `json:"os"`
	User        string   `json:"user"`
	IP          string   `json:"ip"`
	DeclaredIPs []string `json:"declared_ips,omitempty"`
	ObservedIPs []string `json:"observed_ips,omitempty"`
	Model       string   `json:"model"`
	Provider    string   `json:"provider"`
}

func startAgentRegistration(cfg ProbeConfig) {
	if strings.TrimSpace(cfg.HubURL) == "" {
		return
	}
	go func() {
		client := &http.Client{Timeout: 8 * time.Second}
		for {
			rec := collectAgentRegistration(cfg)
			if err := postAgent(client, cfg, "/api/agents/register", rec); err != nil {
				log.Printf("agent registry register failed: %v", err)
			} else {
				log.Printf("agent registered: id=%s type=%s ip=%s", rec.AgentID, rec.AgentType, rec.IP)
			}
			ticker := time.NewTicker(30 * time.Second)
			<-ticker.C
			ticker.Stop()
			heartbeat := collectAgentRegistration(cfg)
			if err := postAgent(client, cfg, "/api/agents/heartbeat", map[string]any{
				"agent_id":     heartbeat.AgentID,
				"ip":           heartbeat.IP,
				"observed_ips": heartbeat.ObservedIPs,
				"model":        heartbeat.Model,
				"provider":     heartbeat.Provider,
				"agent_type":   heartbeat.AgentType,
				"alias":        heartbeat.Alias,
				"activity":     time.Now().UTC(),
			}); err != nil {
				log.Printf("agent registry heartbeat failed: %v", err)
			}
		}
	}()
}

func collectAgentRegistration(cfg ProbeConfig) agentRegistration {
	name, _ := os.Hostname()
	usr := ""
	if u, err := user.Current(); err == nil {
		usr = u.Username
	}
	machineSeed := strings.Join([]string{name, usr, runtime.GOOS, runtime.GOARCH}, "|")
	h := sha256.Sum256([]byte(machineSeed))
	machineID := hex.EncodeToString(h[:])[:16]
	agentID := cfg.AgentID
	if agentID == "" {
		agentID = cfg.TenantName + "-" + name
	}
	probeID := "probe-" + machineID
	agentType := cfg.AgentType
	if agentType == "" {
		agentType = "unknown"
	}
	model, provider := "", ""
	// Prefer observed traffic over static config (sidecar sniffing).
	if om := getObservedModel(); om != "" {
		model = om
		if op := getObservedProvider(); op != "" {
			provider = op
		} else if len(cfg.Providers) > 0 {
			provider = cfg.Providers[0].Name
		}
	} else if len(cfg.Providers) > 0 {
		provider = cfg.Providers[0].Name
		model = cfg.Providers[0].DefaultModel
	}
	// Also allow provider-only observation (e.g. Responses API where model captured separately)
	if model != "" && provider == "" {
		if op := getObservedProvider(); op != "" {
			provider = op
		}
	}
	if provider == "" && model == "" {
		if op := getObservedProvider(); op != "" {
			provider = op
		}
	}
	return agentRegistration{
		SessionID:   agentID,
		AgentID:     agentID,
		ProbeID:     probeID,
		MachineID:   machineID,
		MachineName: name,
		Alias:       cfg.AgentAlias,
		AgentType:   agentType,
		ProcessID:   os.Getpid(),
		OS:          runtime.GOOS,
		User:        usr,
		IP:          firstIP(localIPs()),
		DeclaredIPs: append([]string{}, cfg.DeclaredIPs...),
		ObservedIPs: localIPs(),
		Model:       model,
		Provider:    provider,
	}
}

func localIPs() []string {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, in := range ifs {
		if in.Flags&net.FlagUp == 0 || in.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := in.Addrs()
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err == nil && !ip.IsLoopback() && !seen[ip.String()] {
				seen[ip.String()] = true
				out = append(out, ip.String())
			}
		}
	}
	return out
}

func firstIP(ips []string) string {
	for _, value := range ips {
		ip := net.ParseIP(value)
		if ip != nil && ip.To4() != nil && !ip.IsUnspecified() {
			// Prefer a routable IPv4 over IPv6 link-local addresses.
			if !strings.HasPrefix(value, "169.254.") {
				return value
			}
		}
	}
	if len(ips) > 0 {
		return ips[0]
	}
	return ""
}

func localIPv4() string { return firstIP(localIPs()) }

func postAgent(client *http.Client, cfg ProbeConfig, path string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	url := strings.TrimRight(cfg.HubURL, "/") + path
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.TenantKey != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cfg.TenantKey))
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	return nil
}
