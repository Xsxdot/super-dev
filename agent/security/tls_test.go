package security_test

import (
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/security"
)

func TestGenerateSelfSignedCertificateVerifiesForHost(t *testing.T) {
	certPEM, keyPEM, caPEM, err := security.GenerateSelfSigned([]string{"100.64.0.8", "agent.local"})
	require.NoError(t, err)
	require.NotEmpty(t, certPEM)
	require.NotEmpty(t, keyPEM)
	require.Equal(t, certPEM, caPEM)

	block, _ := pem.Decode([]byte(certPEM))
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM([]byte(caPEM)))
	_, err = cert.Verify(x509.VerifyOptions{DNSName: "agent.local", Roots: pool})
	require.NoError(t, err)
}
