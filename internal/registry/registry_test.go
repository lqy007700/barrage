package registry

import (
	"context"
	"testing"
)

func TestStaticRegistry(t *testing.T) {
	r := NewStaticRegistry()
	ctx := context.Background()

	// 注册服务
	info := &ServiceInfo{
		ID:   "edge-1",
		Name: "edge",
		Addr: "192.168.1.1",
		Port: 8080,
	}

	err := r.Register(ctx, info)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// 获取服务
	services, err := r.GetServices(ctx, "edge")
	if err != nil {
		t.Fatalf("GetServices failed: %v", err)
	}
	if len(services) != 1 {
		t.Errorf("expected 1 service, got %d", len(services))
	}

	// 注销服务
	err = r.Deregister(ctx, "edge-1")
	if err != nil {
		t.Fatalf("Deregister failed: %v", err)
	}

	services, err = r.GetServices(ctx, "edge")
	if err != nil {
		t.Fatalf("GetServices failed: %v", err)
	}
	if len(services) != 0 {
		t.Errorf("expected 0 services, got %d", len(services))
	}
}

func TestStaticRegistryMultipleServices(t *testing.T) {
	r := NewStaticRegistry()
	ctx := context.Background()

	// 注册多个服务
	for i := 1; i <= 5; i++ {
		info := &ServiceInfo{
			ID:   "edge-" + string(rune('0'+i)),
			Name: "edge",
			Addr: "192.168.1." + string(rune('0'+i)),
			Port: 8080,
		}
		err := r.Register(ctx, info)
		if err != nil {
			t.Fatalf("Register %d failed: %v", i, err)
		}
	}

	services, err := r.GetServices(ctx, "edge")
	if err != nil {
		t.Fatalf("GetServices failed: %v", err)
	}
	if len(services) != 5 {
		t.Errorf("expected 5 services, got %d", len(services))
	}
}

func TestStaticRegistryHeartbeat(t *testing.T) {
	r := NewStaticRegistry()
	ctx := context.Background()

	info := &ServiceInfo{
		ID:   "gateway-1",
		Name: "gateway",
		Addr: "192.168.1.100",
		Port: 8080,
	}

	r.Register(ctx, info)

	// 心跳不应该报错
	err := r.Heartbeat(ctx, "gateway-1")
	if err != nil {
		t.Errorf("Heartbeat failed: %v", err)
	}

	// 不存在的服务心跳也应该不报错
	err = r.Heartbeat(ctx, "not-exist")
	if err != nil {
		t.Errorf("Heartbeat for not-exist should not fail: %v", err)
	}
}

func TestServiceRegistrar(t *testing.T) {
	r := NewStaticRegistry()
	ctx := context.Background()

	info := &ServiceInfo{
		ID:   "test-service",
		Name: "test",
		Addr: "localhost",
		Port: 9090,
	}

	registrar := NewServiceRegistrar(r, info)
	err := registrar.Start(ctx)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// 检查是否注册成功
	services, _ := r.GetServices(ctx, "test")
	if len(services) != 1 {
		t.Errorf("expected 1 registered service, got %d", len(services))
	}

	registrar.Stop()
}

func TestGetAllServices(t *testing.T) {
	r := NewStaticRegistry()
	ctx := context.Background()

	// 注册不同类型的服务
	r.Register(ctx, &ServiceInfo{ID: "edge-1", Name: "edge", Addr: "192.168.1.1", Port: 8080})
	r.Register(ctx, &ServiceInfo{ID: "gateway-1", Name: "gateway", Addr: "192.168.1.2", Port: 8080})
	r.Register(ctx, &ServiceInfo{ID: "gateway-2", Name: "gateway", Addr: "192.168.1.3", Port: 8080})

	all := r.GetAllServices()
	if len(all) != 3 {
		t.Errorf("expected 3 services, got %d", len(all))
	}
}
