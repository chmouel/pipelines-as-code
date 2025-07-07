package concurrency

import (
	"context"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/concurrency/test"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
)

// Client interface for etcd operations.
// Basic key-value operations.
// Lease operations.
// Transaction operations.
// Watch operations.
// Close connection.
type Client interface {
	// Basic key-value operations
	Put(ctx context.Context, key, value string, opts ...clientv3.OpOption) (*clientv3.PutResponse, error)
	Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error)
	Delete(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.DeleteResponse, error)

	// Lease operations
	Grant(ctx context.Context, ttl int64) (*clientv3.LeaseGrantResponse, error)
	Revoke(ctx context.Context, id clientv3.LeaseID) (*clientv3.LeaseRevokeResponse, error)
	KeepAlive(ctx context.Context, id clientv3.LeaseID) (<-chan *clientv3.LeaseKeepAliveResponse, error)

	// Transaction operations
	Txn(ctx context.Context) clientv3.Txn

	// Watch operations
	Watch(ctx context.Context, key string, opts ...clientv3.OpOption) clientv3.WatchChan

	// Close connection
	Close() error
}

// Config for etcd client.
type Config struct {
	Endpoints   []string
	DialTimeout time.Duration
	Username    string
	Password    string
	TLSConfig   *TLSConfig
	Enabled     bool
	Mode        string // "memory", "mock", or "etcd"
}

// etcdClient implements the Client interface.
// NewClient creates a new etcd client.
type etcdClient struct {
	client *clientv3.Client
	logger *zap.SugaredLogger
}

func NewClient(cfg *Config, logger *zap.SugaredLogger) (Client, error) {
	// Check if we should use mock mode
	if cfg.Mode == "mock" {
		return test.NewMockClient(logger), nil
	}

	clientCfg := clientv3.Config{
		Endpoints:   cfg.Endpoints,
		DialTimeout: cfg.DialTimeout,
		Username:    cfg.Username,
		Password:    cfg.Password,
	}

	// if cfg.TLSConfig != nil {
	// TODO: Configure TLS if needed.
	// TLS configuration logic should be implemented here if needed.
	// }

	client, err := clientv3.New(clientCfg)
	if err != nil {
		return nil, err
	}

	return &etcdClient{
		client: client,
		logger: logger,
	}, nil
}

func (c *etcdClient) Put(ctx context.Context, key, value string, opts ...clientv3.OpOption) (*clientv3.PutResponse, error) {
	c.logger.Debugf("etcd PUT: %s = %s", key, value)
	return c.client.Put(ctx, key, value, opts...)
}

func (c *etcdClient) Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	c.logger.Debugf("etcd GET: %s", key)
	return c.client.Get(ctx, key, opts...)
}

func (c *etcdClient) Delete(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.DeleteResponse, error) {
	c.logger.Debugf("etcd DELETE: %s", key)
	return c.client.Delete(ctx, key, opts...)
}

func (c *etcdClient) Grant(ctx context.Context, ttl int64) (*clientv3.LeaseGrantResponse, error) {
	c.logger.Debugf("etcd GRANT lease: %d seconds", ttl)
	return c.client.Grant(ctx, ttl)
}

func (c *etcdClient) Revoke(ctx context.Context, id clientv3.LeaseID) (*clientv3.LeaseRevokeResponse, error) {
	c.logger.Debugf("etcd REVOKE lease: %x", id)
	return c.client.Revoke(ctx, id)
}

func (c *etcdClient) KeepAlive(ctx context.Context, id clientv3.LeaseID) (<-chan *clientv3.LeaseKeepAliveResponse, error) {
	c.logger.Debugf("etcd KEEPALIVE lease: %x", id)
	return c.client.KeepAlive(ctx, id)
}

func (c *etcdClient) Txn(ctx context.Context) clientv3.Txn {
	c.logger.Debug("etcd TXN")
	return c.client.Txn(ctx)
}

func (c *etcdClient) Watch(ctx context.Context, key string, opts ...clientv3.OpOption) clientv3.WatchChan {
	c.logger.Debugf("etcd WATCH: %s", key)
	return c.client.Watch(ctx, key, opts...)
}

func (c *etcdClient) Close() error {
	c.logger.Debug("closing etcd client")
	return c.client.Close()
}
