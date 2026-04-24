package daemon

import (
	"context"
	"errors"
	"testing"
)

// mockPublisher is a test ExposurePublisher.
type mockPublisher struct {
	published   []publishCall
	unpublished []unpublishCall
	publishErr  error
}

type publishCall struct {
	name     string
	targetIP string
	port     int
}

type unpublishCall struct {
	name string
	port int
}

func (m *mockPublisher) Publish(ctx context.Context, name string, targetIP string, port int) (ExposurePublishResult, error) {
	m.published = append(m.published, publishCall{name: name, targetIP: targetIP, port: port})
	if m.publishErr != nil {
		return ExposurePublishResult{}, m.publishErr
	}
	return ExposurePublishResult{URL: "http://" + name, State: "healthy"}, nil
}

func (m *mockPublisher) Unpublish(ctx context.Context, name string, port int) error {
	m.unpublished = append(m.unpublished, unpublishCall{name: name, port: port})
	return nil
}

func TestMultiPublisher_Publish_AllSucceed(t *testing.T) {
	p1 := &mockPublisher{}
	p2 := &mockPublisher{}

	mp := NewMultiPublisher(nil, p1, p2)
	result, err := mp.Publish(context.Background(), "mybox", "10.77.0.5", 8080)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if result.URL != "http://mybox" {
		t.Errorf("URL = %q, want http://mybox", result.URL)
	}
	if len(p1.published) != 1 {
		t.Errorf("p1 published calls = %d, want 1", len(p1.published))
	}
	if len(p2.published) != 1 {
		t.Errorf("p2 published calls = %d, want 1", len(p2.published))
	}
}

func TestMultiPublisher_Publish_FirstFails(t *testing.T) {
	p1 := &mockPublisher{publishErr: errors.New("caddy down")}
	p2 := &mockPublisher{}

	mp := NewMultiPublisher(nil, p1, p2)
	result, err := mp.Publish(context.Background(), "mybox", "10.77.0.5", 8080)
	if err != nil {
		t.Fatalf("Publish should succeed when second publisher works: %v", err)
	}

	if result.URL != "http://mybox" {
		t.Errorf("URL = %q, want http://mybox", result.URL)
	}
}

func TestMultiPublisher_Publish_AllFail(t *testing.T) {
	p1 := &mockPublisher{publishErr: errors.New("fail1")}
	p2 := &mockPublisher{publishErr: errors.New("fail2")}

	mp := NewMultiPublisher(nil, p1, p2)
	_, err := mp.Publish(context.Background(), "mybox", "10.77.0.5", 8080)
	if err == nil {
		t.Fatal("Publish should fail when all publishers fail")
	}
}

func TestMultiPublisher_Unpublish_All(t *testing.T) {
	p1 := &mockPublisher{}
	p2 := &mockPublisher{}

	mp := NewMultiPublisher(nil, p1, p2)
	if err := mp.Unpublish(context.Background(), "mybox", 8080); err != nil {
		t.Fatalf("Unpublish: %v", err)
	}

	if len(p1.unpublished) != 1 {
		t.Errorf("p1 unpublished calls = %d, want 1", len(p1.unpublished))
	}
	if len(p2.unpublished) != 1 {
		t.Errorf("p2 unpublished calls = %d, want 1", len(p2.unpublished))
	}
}

func TestCaddyProxyPublisher_Publish(t *testing.T) {
	// This tests the adapter wrapper - the actual CaddyPublisher is tested in the proxy package
	// We're testing that the adapter correctly maps between the two result types
	p := &CaddyProxyPublisher{} // inner is nil, Publish will fail
	_, err := p.Publish(context.Background(), "test", "10.77.0.5", 8080)
	if err == nil {
		t.Error("expected error with nil inner publisher")
	}
}
