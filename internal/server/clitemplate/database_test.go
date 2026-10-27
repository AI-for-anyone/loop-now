package clitemplate

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"github.com/google/uuid"
	"loop-now/internal/server/dbcli"
	"math/big"
	"testing"
	"time"
)

func TestDatabaseRenderingAndSetAggregation(t *testing.T) {
	catalog, e := Load("")
	if e != nil {
		t.Fatal(e)
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "offline.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, _ := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	for _, cli := range []string{"psql", "mysql"} {
		t.Run(cli, func(t *testing.T) {
			tmpl, _ := catalog.Get(cli)
			fields := map[string]string{"host": "db.example", "user": "synthetic", "password": " a:b\\\"# ", "database": "db", "caCert": ca}
			payload, _, e := tmpl.Render(fields)
			if e != nil {
				t.Fatal(e)
			}
			var parsed dbcli.Fields
			if json.Unmarshal(payload.Files[0].Content, &parsed) != nil || parsed.Password != fields["password"] {
				t.Fatal("password changed")
			}
			project := uuid.New()
			a, b := uuid.New(), uuid.New()
			v1, v2 := uuid.New(), uuid.New()
			combined, e := AggregateDatabase(cli, []DatabaseEntry{{Connection: dbcli.Connection{ProjectID: &project, AccountID: a, Name: "A", VersionID: v1}, Payload: payload}, {Connection: dbcli.Connection{ProjectID: &project, AccountID: b, Name: "B", VersionID: v2}, Payload: payload}})
			if e != nil || len(combined.Files) != 1 || combined.Files[0].Path != dbcli.ConfigPath(cli) {
				t.Fatal("aggregate", e)
			}
			var set []dbcli.Connection
			if json.Unmarshal(combined.Files[0].Content, &set) != nil || len(set) != 2 {
				t.Fatal("same CLI overwrite")
			}
			if set[0].AccountID == set[1].AccountID || !bytes.Equal([]byte(set[1].Fields.Password), []byte(fields["password"])) {
				t.Fatal("capture mismatch")
			}
			for _, bad := range []map[string]string{{"host": "db.example", "user": "u", "password": "synthetic", "database": "d"}, {"host": "db.example", "user": "u", "password": "synthetic", "database": "d", "caCert": "invalid"}, {"host": "/socket", "user": "u", "password": "synthetic", "database": "d", "caCert": ca}, {"host": "db.example", "user": "u", "password": "synthetic", "database": "d", "caCert": ca, "clientCert": ca}} {
				if _, _, e = tmpl.Render(bad); e == nil {
					t.Fatal("invalid TLS accepted")
				}
			}
		})
	}
}
