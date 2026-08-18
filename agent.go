// Device Monitor - Agent (Go edition)
// -------------------------------------
// A single native executable - nothing to install on the target
// device. Just copy the binary (and agent-config.json) over and run
// it. No compiler, no runtime, no package manager needed on that
// device.
//
// Settings live in "agent-config.json", a plain text file the binary
// looks for in its own directory at startup. Edit that file with any
// text editor - no need to touch this source or rebuild anything. If
// the config file is missing, the binary writes a starter one next to
// itself and keeps running with placeholder values (which will just
// fail to connect until you fill them in - that's fine, it never
// stops retrying).
//
// (For maintainers: rebuild for other platforms with e.g.
//   GOOS=windows GOARCH=amd64 go build -o agent-windows-amd64.exe agent.go
// see README for the full list of targets.)

package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ------------------------------------------------------------------
// Defaults - only used if agent-config.json can't be read/created.
// Normally you never edit this block; edit agent-config.json instead.
// ------------------------------------------------------------------
const (
	defaultServerURL         = "https://YOUR-MAIN-DEVICE-ADDRESS:8765"
	defaultToken             = "change-me-to-a-long-random-string"
	defaultHeartbeatSeconds  = 45
	defaultIPCheckSeconds    = 15
	defaultInitialRetryMin   = 3
	defaultInitialRetryMax   = 60
	defaultRequestTimeoutSec = 6
)

type Config struct {
	ServerURL         string `json:"server_url"`
	Token             string `json:"token"`
	DeviceName        string `json:"device_name"` // "" = auto-use machine hostname
	HeartbeatSeconds  int    `json:"heartbeat_seconds"`
	IPCheckSeconds    int    `json:"ip_check_seconds"`
	InitialRetryMin   int    `json:"initial_retry_min_seconds"`
	InitialRetryMax   int    `json:"initial_retry_max_seconds"`
	RequestTimeoutSec int    `json:"request_timeout_seconds"`
}

var cfg Config
var client *http.Client

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func configPath() string {
	return filepath.Join(exeDir(), "agent-config.json")
}

// buildHTTPClient wires up TLS trust. If a "server-cert.pem" file sits
// next to the binary, it's pinned as the only trusted certificate -
// this is how you trust your own self-signed server cert without a
// public certificate authority. If no pin file is present, the system's
// normal CA trust store is used instead (fine for a real cert, e.g.
// from Let's Encrypt via a DDNS hostname).
func buildHTTPClient() *http.Client {
	pinPath := filepath.Join(exeDir(), "server-cert.pem")
	data, err := os.ReadFile(pinPath)
	if err != nil {
		return &http.Client{Timeout: requestTimeout()}
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		fmt.Println("[agent] warning: couldn't parse server-cert.pem, ignoring it.")
		return &http.Client{Timeout: requestTimeout()}
	}
	fmt.Println("[agent] pinned TLS trust to server-cert.pem")
	return &http.Client{
		Timeout: requestTimeout(),
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}
}

func loadConfig() Config {
	c := Config{
		ServerURL:         defaultServerURL,
		Token:             defaultToken,
		DeviceName:        "",
		HeartbeatSeconds:  defaultHeartbeatSeconds,
		IPCheckSeconds:    defaultIPCheckSeconds,
		InitialRetryMin:   defaultInitialRetryMin,
		InitialRetryMax:   defaultInitialRetryMax,
		RequestTimeoutSec: defaultRequestTimeoutSec,
	}

	path := configPath()
	data, err := os.ReadFile(path)
	if err != nil {
		// no config file yet - write a starter one so the user has
		// something to edit, then keep running on defaults
		out, _ := json.MarshalIndent(c, "", "  ")
		_ = os.WriteFile(path, out, 0644)
		fmt.Printf("[agent] no config found, wrote a starter one at %s - edit it and restart.\n", path)
		return c
	}

	if err := json.Unmarshal(data, &c); err != nil {
		fmt.Printf("[agent] couldn't parse %s (%v), using defaults.\n", path, err)
	}
	return c
}

func heartbeatInterval() time.Duration { return time.Duration(cfg.HeartbeatSeconds) * time.Second }
func ipCheckInterval() time.Duration   { return time.Duration(cfg.IPCheckSeconds) * time.Second }
func initialRetryMin() time.Duration   { return time.Duration(cfg.InitialRetryMin) * time.Second }
func initialRetryMax() time.Duration   { return time.Duration(cfg.InitialRetryMax) * time.Second }
func requestTimeout() time.Duration    { return time.Duration(cfg.RequestTimeoutSec) * time.Second }

func deviceName() string {
	if cfg.DeviceName != "" {
		return cfg.DeviceName
	}
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown-device"
	}
	return h
}

// privateIP finds the local IP the OS would use to reach the internet.
// Doesn't actually send any data anywhere.
func privateIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr := conn.LocalAddr().(*net.UDPAddr)
	return addr.IP.String()
}

// publicIPClient is separate from the pinned server client: it talks to
// normal internet services (ipify etc) which have real, publicly
// trusted certificates, so it uses the system's default CA trust
// instead of the pin used for our own self-signed server.
var publicIPClient = &http.Client{Timeout: defaultRequestTimeoutSec * time.Second}

func publicIP() string {
	if resp, err := publicIPClient.Get("https://api.ipify.org?format=json"); err == nil {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var parsed struct {
			IP string `json:"ip"`
		}
		if json.Unmarshal(body, &parsed) == nil && net.ParseIP(parsed.IP) != nil {
			return parsed.IP
		}
	}

	if resp, err := publicIPClient.Get("https://ifconfig.me/ip"); err == nil {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		ip := string(bytes.TrimSpace(body))
		if net.ParseIP(ip) != nil {
			return ip
		}
	}

	return ""
}

func sendHeartbeat() bool {
	payload := map[string]string{
		"token":      cfg.Token,
		"name":       deviceName(),
		"private_ip": privateIP(),
		"public_ip":  publicIP(),
	}
	data, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", trimSlash(cfg.ServerURL)+"/api/heartbeat", bytes.NewReader(data))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func waitForFirstConnection() {
	delay := initialRetryMin()
	attempt := 1
	for {
		fmt.Printf("[agent] connecting to server (attempt %d)...\n", attempt)
		if sendHeartbeat() {
			fmt.Println("[agent] connected and registered.")
			return
		}
		attempt++
		time.Sleep(delay)
		delay = time.Duration(float64(delay) * 1.5)
		if delay > initialRetryMax() {
			delay = initialRetryMax()
		}
	}
}

func mainLoop() {
	lastPrivateIP := privateIP()
	lastHeartbeat := time.Time{}

	for {
		now := time.Now()
		currentPrivateIP := privateIP()
		ipChanged := currentPrivateIP != lastPrivateIP
		dueForHeartbeat := now.Sub(lastHeartbeat) >= heartbeatInterval()

		if ipChanged || dueForHeartbeat {
			if sendHeartbeat() {
				lastPrivateIP = currentPrivateIP
				lastHeartbeat = now
				if ipChanged {
					fmt.Printf("[agent] network changed -> %s, server updated.\n", currentPrivateIP)
				}
			} else {
				fmt.Println("[agent] heartbeat failed, will retry.")
				if now.Sub(lastHeartbeat) > heartbeatInterval()*5 {
					fmt.Println("[agent] lost contact with server, retrying persistently...")
					waitForFirstConnection()
					lastHeartbeat = time.Now()
					lastPrivateIP = privateIP()
				}
			}
		}

		time.Sleep(ipCheckInterval())
	}
}

func main() {
	cfg = loadConfig()
	client = buildHTTPClient()
	fmt.Printf("[agent] starting as '%s', reporting to %s\n", deviceName(), cfg.ServerURL)
	waitForFirstConnection()
	mainLoop()
}
