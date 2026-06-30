package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// VaultwardenResolver resolves "vault://<item-id>" refs via the Bitwarden CLI
// local REST API (`bw serve`), which fronts a Vaultwarden server (C18). This
// avoids reimplementing Bitwarden's end-to-end crypto: `bw serve` runs locally
// with an unlocked session and exposes decrypted items over loopback HTTP.
//
// Production deployment: a sidecar runs `bw serve --hostname 127.0.0.1`; the
// backend points BaseURL at it. The session is established out-of-band (bw login
// + unlock) and never leaves the host.
type VaultwardenResolver struct {
	baseURL string
	http    *http.Client
}

// vaultwardenHTTPTimeout bounds calls to the local `bw serve` endpoint.
const vaultwardenHTTPTimeout = 10 * time.Second

// NewVaultwardenResolver returns a resolver targeting a `bw serve` endpoint.
func NewVaultwardenResolver(baseURL string) *VaultwardenResolver {
	return &VaultwardenResolver{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: vaultwardenHTTPTimeout},
	}
}

// bwItem is the subset of the `bw serve` item response we consume.
type bwItem struct {
	Success bool `json:"success"`
	Data    struct {
		Login struct {
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"login"`
		Fields []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"fields"`
	} `json:"data"`
}

func (v *VaultwardenResolver) Resolve(ctx context.Context, ref string) (Credential, error) {
	id, ok := strings.CutPrefix(ref, "vault://")
	if !ok {
		return Credential{}, fmt.Errorf("%w: VaultwardenResolver expects vault:// ref, got %q", ErrNotFound, ref)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.baseURL+"/object/item/"+id, nil)
	if err != nil {
		return Credential{}, err
	}
	resp, err := v.http.Do(req)
	if err != nil {
		return Credential{}, fmt.Errorf("vaultwarden: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return Credential{}, fmt.Errorf("%w: %s", ErrNotFound, ref)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Credential{}, fmt.Errorf("vaultwarden: status %d: %s", resp.StatusCode, string(body))
	}
	var item bwItem
	if err := json.Unmarshal(body, &item); err != nil {
		return Credential{}, fmt.Errorf("vaultwarden: decode: %w", err)
	}
	if !item.Success {
		return Credential{}, fmt.Errorf("%w: bw reported failure for %s", ErrNotFound, ref)
	}
	cred := Credential{
		Username: item.Data.Login.Username,
		Password: item.Data.Login.Password,
	}
	for _, f := range item.Data.Fields {
		if f.Name == "api_key" {
			cred.APIKey = f.Value
		}
	}
	return cred, nil
}

// bwCreated is the subset of `bw serve` create-item response we consume.
type bwCreated struct {
	Success bool `json:"success"`
	Data    struct {
		ID string `json:"id"`
	} `json:"data"`
}

// Put creates a login item via `bw serve` and returns its "vault://<id>" ref
// (LLD-08 §6). Used to persist a rotated agent UI password so the platform DB
// only ever stores the pointer, never the plaintext.
//
// Credential layout: the (Username, Password) pair maps to Bitwarden's login
// item login.username / login.password (basic auth). A standalone API/master
// key (Credential.APIKey only) is stored as a custom field named "api_key" —
// that's what Resolve reads back into Credential.APIKey. Both forms round-trip
// (Put → Resolve) so a master-key-only Put (the gateway onboarding path,
// `Credential{APIKey: "sk-…"}`) is recoverable, and a user/password Put
// (agent UI rotation) is unchanged.
func (v *VaultwardenResolver) Put(ctx context.Context, name string, cred Credential) (string, error) {
	payload := map[string]any{
		"type":  1, // login
		"name":  name,
		"login": map[string]string{"username": cred.Username, "password": cred.Password},
	}
	if cred.APIKey != "" {
		payload["fields"] = []map[string]string{
			{"name": "api_key", "value": cred.APIKey},
		}
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.baseURL+"/object/item", bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := v.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("vaultwarden put: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("vaultwarden put: status %d: %s", resp.StatusCode, string(body))
	}
	var created bwCreated
	if err := json.Unmarshal(body, &created); err != nil {
		return "", fmt.Errorf("vaultwarden put: decode: %w", err)
	}
	if !created.Success || created.Data.ID == "" {
		return "", fmt.Errorf("vaultwarden put: bw reported failure or empty id")
	}
	return "vault://" + created.Data.ID, nil
}

// Delete removes an item via `bw serve` (DELETE /object/item/<id>) so a deleted
// or rotated secret doesn't orphan in the vault. Idempotent: a 404 (already
// gone) reads as success.
func (v *VaultwardenResolver) Delete(ctx context.Context, ref string) error {
	id, ok := strings.CutPrefix(ref, "vault://")
	if !ok {
		return fmt.Errorf("%w: VaultwardenResolver expects vault:// ref, got %q", ErrNotFound, ref)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, v.baseURL+"/object/item/"+id, nil)
	if err != nil {
		return err
	}
	resp, err := v.http.Do(req)
	if err != nil {
		return fmt.Errorf("vaultwarden delete: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return nil // already gone — idempotent
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("vaultwarden delete: status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
