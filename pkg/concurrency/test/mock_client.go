package test

import (
	"context"
	"strings"
	"time"

	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
)

// MockClient for testing.
type MockClient struct {
	Data       map[string]string
	Leases     map[clientv3.LeaseID]time.Time
	LeaseToKey map[clientv3.LeaseID]string
	LeaseQueue []clientv3.LeaseID
	WatchChans map[string]chan clientv3.WatchResponse
	logger     *zap.SugaredLogger
}

func NewMockClient(logger *zap.SugaredLogger) *MockClient {
	return &MockClient{
		Data:       make(map[string]string),
		Leases:     make(map[clientv3.LeaseID]time.Time),
		LeaseToKey: make(map[clientv3.LeaseID]string),
		LeaseQueue: []clientv3.LeaseID{},
		WatchChans: make(map[string]chan clientv3.WatchResponse),
		logger:     logger,
	}
}

func (m *MockClient) Put(_ context.Context, key, value string, _ ...clientv3.OpOption) (*clientv3.PutResponse, error) {
	m.Data[key] = value
	// Trigger watch events
	if ch, exists := m.WatchChans[key]; exists {
		select {
		case ch <- clientv3.WatchResponse{
			Events: []*clientv3.Event{
				{
					Type: mvccpb.PUT,
					Kv:   &mvccpb.KeyValue{Key: []byte(key), Value: []byte(value)},
				},
			},
		}:
		default:
		}
	}
	return &clientv3.PutResponse{}, nil
}

func (m *MockClient) Get(_ context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	resp := &clientv3.GetResponse{}

	// If options are provided, assume it's a prefix query (WithPrefix, WithCountOnly, etc.)
	if len(opts) > 0 {
		// Check if this is a count-only query
		isCountOnly := false
		for range opts {
			// For simplicity, assume any option might be count-only
			isCountOnly = true
			break
		}

		// Handle prefix queries
		count := 0
		var kvs []*mvccpb.KeyValue
		for k, v := range m.Data {
			if strings.HasPrefix(k, key) {
				// For count-only queries, exclude /info keys
				// For regular prefix queries, include all keys
				if !isCountOnly || !strings.HasSuffix(k, "/info") {
					count++
					kvs = append(kvs, &mvccpb.KeyValue{
						Key:   []byte(k),
						Value: []byte(v),
					})
				}
			}
		}
		resp.Kvs = kvs
		resp.Count = int64(count)
		m.logger.Debugf("Mock GET prefix query for '%s': found %d keys (countOnly=%v)", key, count, isCountOnly)
		for k, v := range m.Data {
			if strings.HasPrefix(k, key) && (!isCountOnly || !strings.HasSuffix(k, "/info")) {
				m.logger.Debugf("  Key: %s, Value: %s", k, v)
			}
		}
	} else {
		// Handle exact key queries
		if value, exists := m.Data[key]; exists {
			resp.Kvs = []*mvccpb.KeyValue{
				{Key: []byte(key), Value: []byte(value)},
			}
			resp.Count = 1
		}
	}

	return resp, nil
}

func (m *MockClient) Delete(_ context.Context, key string, _ ...clientv3.OpOption) (*clientv3.DeleteResponse, error) {
	delete(m.Data, key)
	// Trigger watch events
	if ch, exists := m.WatchChans[key]; exists {
		select {
		case ch <- clientv3.WatchResponse{
			Events: []*clientv3.Event{
				{
					Type: mvccpb.DELETE,
					Kv:   &mvccpb.KeyValue{Key: []byte(key)},
				},
			},
		}:
		default:
		}
	}
	return &clientv3.DeleteResponse{}, nil
}

func (m *MockClient) Grant(_ context.Context, ttl int64) (*clientv3.LeaseGrantResponse, error) {
	leaseID := clientv3.LeaseID(time.Now().UnixNano())
	m.Leases[leaseID] = time.Now().Add(time.Duration(ttl) * time.Second)
	m.LeaseQueue = append(m.LeaseQueue, leaseID)
	m.logger.Debugf("Mock GRANT: created lease %x", leaseID)
	return &clientv3.LeaseGrantResponse{ID: leaseID, TTL: ttl}, nil
}

func (m *MockClient) Revoke(_ context.Context, id clientv3.LeaseID) (*clientv3.LeaseRevokeResponse, error) {
	delete(m.Leases, id)
	// Remove the key associated with this lease ID
	if key, exists := m.LeaseToKey[id]; exists {
		delete(m.Data, key)
		delete(m.LeaseToKey, id)
	}
	return &clientv3.LeaseRevokeResponse{}, nil
}

func (m *MockClient) KeepAlive(_ context.Context, id clientv3.LeaseID) (<-chan *clientv3.LeaseKeepAliveResponse, error) {
	ch := make(chan *clientv3.LeaseKeepAliveResponse, 1)
	ch <- &clientv3.LeaseKeepAliveResponse{ID: id, TTL: 3600}
	return ch, nil
}

func (m *MockClient) Txn(_ context.Context) clientv3.Txn {
	// Simple mock transaction - in real tests we'd implement this properly
	return &mockTxn{client: m}
}

func (m *MockClient) Watch(_ context.Context, key string, _ ...clientv3.OpOption) clientv3.WatchChan {
	ch := make(chan clientv3.WatchResponse, 10)
	m.WatchChans[key] = ch
	return ch
}

func (m *MockClient) Close() error {
	return nil
}

type mockTxn struct {
	client          *MockClient
	ops             []clientv3.Op
	ifKey           string
	ifCreateRevZero bool
}

func (m *mockTxn) If(cmps ...clientv3.Cmp) clientv3.Txn {
	// Just store the key for existence check
	if len(cmps) > 0 && len(cmps[0].KeyBytes()) > 0 {
		m.ifKey = string(cmps[0].KeyBytes())
		m.ifCreateRevZero = true
	}
	return m
}

func (m *mockTxn) Then(ops ...clientv3.Op) clientv3.Txn {
	m.ops = ops
	return m
}

func (m *mockTxn) Else(_ ...clientv3.Op) clientv3.Txn {
	return m
}

func (m *mockTxn) Commit() (*clientv3.TxnResponse, error) {
	succeeded := true
	if m.ifCreateRevZero && m.ifKey != "" {
		if _, exists := m.client.Data[m.ifKey]; exists {
			succeeded = false
		}
	}
	if succeeded {
		for _, op := range m.ops {
			if op.IsPut() {
				key := string(op.KeyBytes())
				value := string(op.ValueBytes())
				m.client.Data[key] = value
				m.client.logger.Debugf("Mock TXN PUT: %s = %s", key, value)
				// Store lease ID to key mapping for later cleanup
				if strings.Contains(key, "/pac/leases/") && len(m.client.LeaseQueue) > 0 {
					leaseID := m.client.LeaseQueue[0]
					m.client.LeaseQueue = m.client.LeaseQueue[1:]
					m.client.LeaseToKey[leaseID] = key
					m.client.logger.Debugf("Mock TXN: mapped lease %x to key %s", leaseID, key)
				}
			}
		}
	}
	m.client.logger.Debugf("Mock TXN Commit: succeeded=%v", succeeded)

	// Debug: print all keys in Data map
	m.client.logger.Debugf("Mock Data map contents after transaction:")
	for k, v := range m.client.Data {
		m.client.logger.Debugf("  %s = %s", k, v)
	}

	return &clientv3.TxnResponse{Succeeded: succeeded}, nil
}
