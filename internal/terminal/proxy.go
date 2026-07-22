// Package terminal provides a WebSocket SSH proxy for browser-based terminal access to agent VMs.
package terminal

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
)

var wsKeyGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// HandleTerminal upgrades to WebSocket and proxies SSH to the agent via jump host.
func HandleTerminal(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("[terminal] panic: %v", p)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	}()
	// Strip "/v1/terminal/" prefix to get agent IP
	agentIP := r.URL.Path
	if idx := len("/v1/terminal/"); len(agentIP) > idx {
		agentIP = agentIP[idx:]
	}
	if !regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}$`).MatchString(agentIP) {
		http.Error(w, "invalid IP", http.StatusBadRequest)
		return
	}
	log.Printf("[terminal] agent=%s", agentIP)

	// Quick test: return text to verify routing works
	_, ok := w.(http.Hijacker)
	if !ok {
		log.Printf("[terminal] hijack not supported for %T", w)
		http.Error(w, "hijack not supported: "+r.Proto, http.StatusInternalServerError)
		return
	}

	// Manual WebSocket upgrade
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "missing websocket key", http.StatusBadRequest)
		return
	}
	h := sha1.New()
	h.Write([]byte(key + wsKeyGUID))
	acceptKey := base64.StdEncoding.EncodeToString(h.Sum(nil))

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}
	conn, bufrw, err := hj.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer conn.Close()

	bufrw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	bufrw.WriteString("Upgrade: websocket\r\n")
	bufrw.WriteString("Connection: Upgrade\r\n")
	bufrw.WriteString("Sec-WebSocket-Accept: " + acceptKey + "\r\n\r\n")
	bufrw.Flush()

	// Build SSH command: use jump host if configured, otherwise direct
	jump := os.Getenv("JUMP_HOST")
	jpPass := os.Getenv("JUMP_HOST_PASSWORD")
	agentUser := os.Getenv("AGENT_SSH_USER")
	agentPass := os.Getenv("AGENT_SSH_PASSWORD")
	if agentUser == "" {
		agentUser = "vmware"
	}
	if agentPass == "" {
		agentPass = "VMware1!"
	}

	var sshCmd *exec.Cmd
	if jump != "" {
		inner := fmt.Sprintf("sshpass -p %s ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -tt %s@%s",
			agentPass, agentUser, agentIP)
		sshCmd = exec.Command("sshpass", "-p", jpPass,
			"ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-tt",
			jump, inner)
	} else {
		sshCmd = exec.Command("sshpass", "-p", agentPass,
			"ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-tt",
			agentUser+"@"+agentIP)
	}
	cmd := sshCmd
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		writeWS(conn, []byte("SSH failed: "+err.Error()+"\r\n"))
		return
	}

	// stdout → websocket
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				writeWS(conn, buf[:n])
			}
			if err != nil {
				break
			}
		}
	}()

	// websocket → stdin
	reader := bufio.NewReader(conn)
	for {
		frame, err := readWS(reader)
		if err != nil {
			break
		}
		stdin.Write(frame)
	}
	cmd.Process.Kill()
	cmd.Wait()
}

func writeWS(conn net.Conn, data []byte) {
	frame := make([]byte, 0, len(data)+10)
	frame = append(frame, 0x82) // binary frame, FIN
	if len(data) < 126 {
		frame = append(frame, byte(len(data)))
	} else if len(data) < 65536 {
		frame = append(frame, 126, byte(len(data)>>8), byte(len(data)))
	}
	frame = append(frame, data...)
	conn.Write(frame)
}

func readWS(r *bufio.Reader) ([]byte, error) {
	b0, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if b0&0x0F == 0x8 { // close frame
		return nil, errors.New("close")
	}
	b1, err := r.ReadByte() // MASK(1) + payload_len(7)
	if err != nil {
		return nil, err
	}
	length := int64(b1 & 0x7F)
	if length == 126 {
		lb := make([]byte, 2)
		r.Read(lb)
		length = int64(uint16(lb[0])<<8 | uint16(lb[1]))
	} else if length == 127 {
		lb := make([]byte, 8)
		r.Read(lb)
		length = int64(uint64(lb[0])<<56 | uint64(lb[1])<<48 | uint64(lb[2])<<40 | uint64(lb[3])<<32 |
			uint64(lb[4])<<24 | uint64(lb[5])<<16 | uint64(lb[6])<<8 | uint64(lb[7]))
	}
	mask := make([]byte, 4)
	r.Read(mask)
	payload := make([]byte, length)
	r.Read(payload)
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return payload, nil
}

var _ io.Reader
