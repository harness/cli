package nexus

import (
	"net"
	"net/url"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
)

type nexusKeychain struct {
	username string
	password string
	hostname string
}

// NewNexusKeychain accepts hostname with or without a port: Resolve matches on
// the port-less hostname, and the Docker connector port differs from the REST
// endpoint port (e.g. nexus:8081 vs nexus:5000).
func NewNexusKeychain(username, password, hostname string) authn.Keychain {
	if h, _, err := net.SplitHostPort(hostname); err == nil {
		hostname = h
	}
	return nexusKeychain{
		username: username,
		password: password,
		hostname: hostname,
	}
}

func (n nexusKeychain) Resolve(r authn.Resource) (authn.Authenticator, error) {
	serverURL, err := url.Parse("https://" + r.String())
	if err != nil {
		return authn.Anonymous, nil
	}

	if n.username == "" || n.password == "" {
		return authn.Anonymous, nil
	}

	if strings.EqualFold(serverURL.Hostname(), n.hostname) {
		return nexusAuthenticator{n.username, n.password}, nil
	}
	return authn.Anonymous, nil
}

type nexusAuthenticator struct{ username, password string }

func (n nexusAuthenticator) Authorization() (*authn.AuthConfig, error) {
	return &authn.AuthConfig{
		Username: n.username,
		Password: n.password,
	}, nil
}
