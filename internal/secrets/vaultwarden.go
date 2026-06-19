package secrets

import (
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

// NewVaultwardenResolver returns a resolver targeting a `bw serve` endpoint.
func NewVaultwardenResolver(baseURL string) *VaultwardenResolver {
	return &VaultwardenResolver{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 10 * time.Second},
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
