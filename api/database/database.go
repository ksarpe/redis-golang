package database

import (
	"context"
	"net"
	"os"
	"time"
	"fmt"
	"errors"
	"crypto/tls"
	"crypto/x509"
	radix "github.com/mediocregopher/radix/v4"
)

var (
	errCACertificate = errors.New("unable to append the CA certificate")
	errTypeAssertion = errors.New("type assertion to *net.Dialer failed")
)

const (
	PingInterval         = -1 // Equivalent to PoolPingInterval(0)
	MinReconnectInterval = 125 * time.Millisecond
	MaxReconnectInterval = 4 * time.Second
	PoolSize             = 1
)

type ClientInterface interface {
	// Close closes the connection.
	Close() error

	// Do performs an Action, returning any error.
	Do(action radix.Action) error
}

type ClientProducerInterface interface {
	// Create a new client and check the connection.
	NewClient(addr string, clientOpts *ClientOptions) (ClientInterface, error)
}

type RadixV4ClientsProducer struct{}

// Client structure representing a client connection to redis.
type Client struct {
	pool radix.Client
}

type ClientOptions struct {
	// TLS.
	TLSEnabled        bool
	CaCert            string
	ClientCert        string
	ClientKey         string
	SubjectCommonName string

	// ACL.
	ACLEnabled bool
	Username   string
	Password   string

	// Timeouts.
	DialConnectTimeout time.Duration
	DialWriteTimeout   time.Duration
	DialReadTimeout    time.Duration
}

var Ctx = context.Background()

func (prod RadixV4ClientsProducer) NewClient(addr string) (ClientInterface, error){

	clientOpts := ClientOptions{
		TLSEnabled:false,
		CaCert:"",
		ClientCert:"",
		ClientKey:"",
		SubjectCommonName:"",

		// ACL.
		ACLEnabled:false,
		Username:"",
		Password:"",

		// Timeouts.
		DialConnectTimeout: 10 * time.Second,
		DialWriteTimeout: 1 * time.Second,
		DialReadTimeout: 1 * time.Second,
		}
	dialer := radix.Dialer{
		AuthUser: clientOpts.Username,
		AuthPass: clientOpts.Password,
		NetDialer: &net.Dialer{Timeout: clientOpts.DialConnectTimeout},
	}

	if clientOpts.TLSEnabled {
		tlsConfig, err := createTLSConfig(&clientOpts)
		if err != nil {
			return nil, err
		}
		netDialer, ok := dialer.NetDialer.(*net.Dialer)
		if !ok {
			return nil, errTypeAssertion
		}
		dialer.NetDialer = &tls.Dialer{
			NetDialer: netDialer,
			Config:    tlsConfig,
		}
	}

	poolCfg := radix.PoolConfig{
		Dialer: dialer,
		Size: PoolSize,
		PingInterval: PingInterval,
		MinReconnectInterval: MinReconnectInterval,
		MaxReconnectInterval: MaxReconnectInterval,
	}

	pool, err := poolCfg.New(context.Background(), "tcp", addr)
	if err != nil{
		return nil, fmt.Errorf("radix poolCfg.New err: %w", err)
	}

	c := &Client{pool: pool}

	ipAddr := ""
	ipAddr, _, err = net.SplitHostPort(addr)
	if err != nil{
		c.Close()
		return nil, fmt.Errorf("failed split address, closing client connection, err:%w", err)
	}

	err = c.Do(radix.Cmd(nil, "CONFIG", "SET", "cluster-announce-ip", ipAddr))
	if err != nil {
		c.Close()

		return nil, fmt.Errorf("failed to CONFIG SET, closing client connection, err:%w", err)
	}

	// The masterauth option forces replicas to authenticate with their master
	// before being allowed to replicate data. It cannot be set as part of
	// config since the value is dynamically set in a K8s secret and must be
	// fetched at runtime.
	if clientOpts.ACLEnabled {
		err := c.Do(radix.Cmd(nil, "CONFIG", "SET", "masterauth", clientOpts.Password))
		if err != nil {
			c.Close()

			return nil, fmt.Errorf("failed to CONFIG SET, closing client connection, err:%w", err)
		}
	}

	return c, nil
}

func createTLSConfig(opts *ClientOptions) (*tls.Config, error) {
	var tlsconfig tls.Config

	certBytes, err := os.ReadFile(opts.CaCert)
	if err != nil {
		return nil, fmt.Errorf("failed to read file from %s, err: %w", opts.CaCert, err)
	}

	// CertPool is a set of certificates. NewCertPool() returns a new, empty CertPool.
	caCertPool := x509.NewCertPool()

	// AppendCertsFromPEM attempts to parse the PEM encoded certificate.
	// It appends any certificate found using certBytes and reports whether
	//  the certificate were successfully parsed.
	if ok := caCertPool.AppendCertsFromPEM(certBytes); !ok {
		return nil, errCACertificate
	}

	// RootCAs defines the set of root certificate authorities that clients
	// use when verifying certificates. If RootCAs is nil, TLS uses the host's
	// root CA set.
	tlsconfig.RootCAs = caCertPool

	// LoadX509KeyPair reads and parses a public/private key pair from a pair
	// of files. The files must contain PEM encoded data.
	cert, err := tls.LoadX509KeyPair(opts.ClientCert, opts.ClientKey)
	if err != nil {
		return &tlsconfig, fmt.Errorf("failed to read and parse key %s from %s, err: %w",
			opts.ClientKey, opts.ClientCert, err)
	}

	tlsconfig.Certificates = []tls.Certificate{cert}

	// ServerName is used to verify the hostname on the returned KVDB server
	// certificate unless InsecureSkipVerify is given.
	if opts.SubjectCommonName != "" {
		tlsconfig.ServerName = opts.SubjectCommonName
	}

	return &tlsconfig, nil
}

func (c *Client) Close() error {
	err := c.pool.Close()
	if err != nil {
		return fmt.Errorf("failed to close connection, err: %w", err)
	}

	return nil
}

// Do performs an Action, returning any error.
//
//nolint:contextcheck // radix v4 introduced context handling but for now no need to refactor entire call stack
func (c *Client) Do(action radix.Action) error {
	err := c.pool.Do(context.Background(), action)
	if err != nil {
		return fmt.Errorf("failed to perform action %s, err: %w", action, err)
	}

	return nil
}