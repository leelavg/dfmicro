package odf

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"dfmicro/internal/support"
)

const (
	clientCertValidity      = 47 * 24 * time.Hour
	clientCertRenewalWindow = 7 * 24 * time.Hour
	providerServerSAN       = "ocs-provider-server." + providerNamespace + ".svc"
)

type clientCertificate struct {
	key      []byte
	cert     []byte
	identity string
	expires  time.Time
}

func (o *odf) ensureClientTLS(ctx context.Context, providerName, providerKubeconfig string) (string, error) {
	certificate, updated, err := o.storageClientCertificate(ctx, providerName)
	if err != nil {
		return "", err
	}
	if updated {
		secret, err := support.Render(storageClientCertSecretTmpl, map[string]string{
			"ProviderCluster": providerName,
			"Expiry":          strconv.Quote(certificate.expires.Format(time.RFC3339)),
			"ClientSAN":       strconv.Quote(certificate.identity),
			"Certificate":     base64.StdEncoding.EncodeToString(certificate.cert),
			"PrivateKey":      base64.StdEncoding.EncodeToString(certificate.key),
		})
		if err != nil {
			return "", err
		}
		if err := support.ApplyYAML(ctx, o.runner, support.KubeClient, o.kubeconfig, secret); err != nil {
			return "", fmt.Errorf("apply StorageClient certificate Secret: %w", err)
		}
	}

	providerCA, err := o.runner.Run(ctx, support.KubeClient,
		"get", "configmap", "openshift-service-ca.crt", "-n", providerNamespace,
		"-o", `jsonpath={.data.service-ca\.crt}`, "--kubeconfig", providerKubeconfig,
	)
	if err != nil {
		return "", fmt.Errorf("read provider service CA: %w", err)
	}
	serverCA := strings.TrimSpace(providerCA.Stdout)
	if serverCA == "" {
		return "", fmt.Errorf("provider service CA is empty")
	}

	clientCA, err := support.Render(storageConsumerCATmpl, map[string]string{
		"ClientCluster": o.clusterName,
		"Certificate":   strconv.Quote(string(certificate.cert)),
	})
	if err != nil {
		return "", err
	}
	if err := support.ApplyYAML(ctx, o.runner, support.KubeClient, providerKubeconfig, clientCA); err != nil {
		return "", fmt.Errorf("apply StorageConsumer CA ConfigMap: %w", err)
	}

	serverCAConfigMap, err := support.Render(storageClientServerCATmpl, map[string]string{
		"ProviderCluster": providerName,
		"Certificate":     strconv.Quote(serverCA),
	})
	if err != nil {
		return "", err
	}
	if err := support.ApplyYAML(ctx, o.runner, support.KubeClient, o.kubeconfig, serverCAConfigMap); err != nil {
		return "", fmt.Errorf("apply StorageClient server CA ConfigMap: %w", err)
	}

	return certificate.identity, nil
}

func (o *odf) storageClientCertificate(ctx context.Context, providerName string) (clientCertificate, bool, error) {
	secretName := providerName
	result, err := o.runner.Run(ctx, support.KubeClient,
		"get", "secret", secretName, "-n", providerNamespace, "--ignore-not-found",
		"-o", `jsonpath={.data.tls\.crt}`, "--kubeconfig", o.kubeconfig,
	)
	if err != nil {
		return clientCertificate{}, false, fmt.Errorf("read StorageClient certificate: %w", err)
	}
	encodedCert := strings.TrimSpace(result.Stdout)
	if encodedCert != "" {
		result, err = o.runner.Run(ctx, support.KubeClient,
			"get", "secret", secretName, "-n", providerNamespace,
			"-o", `jsonpath={.data.tls\.key}`, "--kubeconfig", o.kubeconfig,
		)
		if err != nil {
			return clientCertificate{}, false, fmt.Errorf("read StorageClient private key: %w", err)
		}
		encodedKey := strings.TrimSpace(result.Stdout)
		certPEM, certErr := base64.StdEncoding.DecodeString(encodedCert)
		keyPEM, keyErr := base64.StdEncoding.DecodeString(encodedKey)
		if certErr == nil && keyErr == nil {
			if cert, parseErr := parseClientCertificate(certPEM); parseErr == nil && cert.NotAfter.After(time.Now().Add(clientCertRenewalWindow)) {
				if _, pairErr := tls.X509KeyPair(certPEM, keyPEM); pairErr == nil {
					return clientCertificate{
						key:      keyPEM,
						cert:     certPEM,
						identity: cert.DNSNames[0],
						expires:  cert.NotAfter,
					}, false, nil
				}
			}
		}
	}

	identity := "ocs-peer-" + o.clusterName
	keyPEM, certPEM, expires, err := generateClientCertificate(identity)
	if err != nil {
		return clientCertificate{}, false, err
	}
	return clientCertificate{key: keyPEM, cert: certPEM, identity: identity, expires: expires}, true, nil
}

func parseClientCertificate(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("invalid client certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse client certificate: %w", err)
	}
	if len(cert.DNSNames) != 1 || cert.DNSNames[0] == "" {
		return nil, fmt.Errorf("client certificate has no unique DNS SAN")
	}
	return cert, nil
}

func generateClientCertificate(identity string) ([]byte, []byte, time.Time, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("generate client private key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("generate client certificate serial: %w", err)
	}
	if serial.Sign() == 0 {
		serial.SetInt64(1)
	}
	now := time.Now()
	expires := now.Add(clientCertValidity)
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: identity},
		DNSNames:              []string{identity},
		NotBefore:             now,
		NotAfter:              expires,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("create client certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("marshal client private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}), expires, nil
}

func (o *odf) patchStorageConsumerTLS(ctx context.Context, providerKubeconfig, clientSAN string) error {
	patch := fmt.Sprintf(`{"spec":{"clientCAConfigMap":{"name":%q},"clientSAN":%q}}`, o.clusterName, clientSAN)
	if _, err := o.runner.Run(ctx, support.KubeClient,
		"patch", "storageconsumer", o.clusterName, "-n", providerNamespace,
		"--type=merge", "-p", patch, "--kubeconfig", providerKubeconfig,
	); err != nil {
		return fmt.Errorf("patch StorageConsumer mTLS fields: %w", err)
	}
	return nil
}

func (o *odf) patchStorageClientTLS(ctx context.Context, providerName string) error {
	patch := fmt.Sprintf(`{"spec":{"clientCertSecret":{"name":%q},"serverCAConfigMap":{"name":%q,"key":"service-ca.crt"},"serverSAN":%q}}`, providerName, providerName, providerServerSAN)
	if _, err := o.runner.Run(ctx, support.KubeClient,
		"patch", "storageclient", providerName,
		"--type=merge", "-p", patch, "--kubeconfig", o.kubeconfig,
	); err != nil {
		return fmt.Errorf("patch StorageClient mTLS fields: %w", err)
	}
	return nil
}
