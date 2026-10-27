package clitemplate

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"loop-now/internal/server/apperr"
	"net/mail"
	"strings"
)

type serviceAccountJSON struct {
	Type                string `json:"type"`
	ProjectID           string `json:"project_id"`
	PrivateKeyID        string `json:"private_key_id"`
	PrivateKey          string `json:"private_key"`
	ClientEmail         string `json:"client_email"`
	ClientID            string `json:"client_id"`
	AuthURI             string `json:"auth_uri,omitempty"`
	TokenURI            string `json:"token_uri"`
	AuthProviderCertURL string `json:"auth_provider_x509_cert_url,omitempty"`
	ClientCertURL       string `json:"client_x509_cert_url,omitempty"`
	UniverseDomain      string `json:"universe_domain,omitempty"`
}

func renderGoogleSA(values map[string]string) ([]PayloadFile, error) {
	invalid := apperr.Invalid("credential.fields", "Google服务账号JSON或目标项目格式无效")
	raw := []byte(values["serviceAccountJson"])
	defer clear(raw)
	var c serviceAccountJSON
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Type != "service_account" || c.ProjectID == "" || c.PrivateKeyID == "" || c.ClientID == "" || c.TokenURI != "https://oauth2.googleapis.com/token" || c.UniverseDomain != "" && c.UniverseDomain != "googleapis.com" {
		return nil, invalid
	}
	address, e := mail.ParseAddress(c.ClientEmail)
	if e != nil || address.Address != c.ClientEmail || !strings.HasSuffix(c.ClientEmail, ".gserviceaccount.com") {
		return nil, invalid
	}
	if c.AuthURI != "" && c.AuthURI != "https://accounts.google.com/o/oauth2/auth" {
		return nil, invalid
	}
	if c.AuthProviderCertURL != "" && c.AuthProviderCertURL != "https://www.googleapis.com/oauth2/v1/certs" {
		return nil, invalid
	}
	if c.ClientCertURL != "" && !strings.HasPrefix(c.ClientCertURL, "https://www.googleapis.com/robot/v1/metadata/x509/") {
		return nil, invalid
	}
	block, rest := pem.Decode([]byte(c.PrivateKey))
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, invalid
	}
	key, e := x509.ParsePKCS8PrivateKey(block.Bytes)
	if e != nil {
		return nil, invalid
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok || rsaKey.N.BitLen() < 2048 || rsaKey.Validate() != nil {
		return nil, invalid
	}
	project := values["defaultProject"]
	if project != "" {
		if len(project) > 200 || strings.TrimSpace(project) != project || strings.ContainsAny(project, "/\\\r\n\x00 ") {
			return nil, invalid
		}
	}
	encoded, e := json.Marshal(c)
	if e != nil {
		return nil, invalid
	}
	files := []PayloadFile{{Path: ".loop-now/gcloud/service-account.json", Content: encoded}}
	if project != "" {
		files = append(files, PayloadFile{Path: ".loop-now/gcloud/default-project", Content: []byte(project)})
	}
	return files, nil
}
