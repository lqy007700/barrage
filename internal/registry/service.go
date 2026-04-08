package registry

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// Registry 注册中心接口
// 支持 Consul, Etcd, Zookeeper 等
type Registry interface {
	// Register 注册服务
	Register(ctx context.Context, info *ServiceInfo) error
	// Deregister 注销服务
	Deregister(ctx context.Context, serviceId string) error
	// Heartbeat 心跳
	Heartbeat(ctx context.Context, serviceId string) error
	// GetServices 获取服务列表
	GetServices(ctx context.Context, serviceName string) ([]*ServiceInfo, error)
}

// ServiceInfo 服务信息
type ServiceInfo struct {
	ID       string            // 服务ID
	Name     string            // 服务名称
	Addr     string            // 服务地址
	Port     int               // 端口
	Tags     []string          // 标签
	Meta     map[string]string // 元数据
}

// ConsulRegistry Consul实现 (示例)
type ConsulRegistry struct {
	addr string
}

// NewConsulRegistry 创建Consul注册中心
func NewConsulRegistry(addr string) *ConsulRegistry {
	return &ConsulRegistry{addr: addr}
}

// Register 注册服务
func (r *ConsulRegistry) Register(ctx context.Context, info *ServiceInfo) error {
	// TODO: 实现 Consul 注册
	log.Printf("[Consul] Register service: %s -> %s:%d", info.Name, info.Addr, info.Port)
	return nil
}

// Deregister 注销服务
func (r *ConsulRegistry) Deregister(ctx context.Context, serviceId string) error {
	log.Printf("[Consul] Deregister service: %s", serviceId)
	return nil
}

// Heartbeat 心跳
func (r *ConsulRegistry) Heartbeat(ctx context.Context, serviceId string) error {
	return nil
}

// GetServices 获取服务列表
func (r *ConsulRegistry) GetServices(ctx context.Context, serviceName string) ([]*ServiceInfo, error) {
	return nil, nil
}

// StaticRegistry 静态注册中心 (用于测试/简单部署)
type StaticRegistry struct {
	mu       sync.RWMutex
	services map[string]*ServiceInfo
}

// NewStaticRegistry 创建静态注册中心
func NewStaticRegistry() *StaticRegistry {
	return &StaticRegistry{
		services: make(map[string]*ServiceInfo),
	}
}

// Register 注册服务
func (r *StaticRegistry) Register(ctx context.Context, info *ServiceInfo) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.services[info.ID] = info
	log.Printf("[Static] Register service: %s -> %s:%d", info.Name, info.Addr, info.Port)
	return nil
}

// Deregister 注销服务
func (r *StaticRegistry) Deregister(ctx context.Context, serviceId string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.services, serviceId)
	return nil
}

// Heartbeat 心跳 (静态注册中心不需要)
func (r *StaticRegistry) Heartbeat(ctx context.Context, serviceId string) error {
	return nil
}

// GetServices 获取服务列表
func (r *StaticRegistry) GetServices(ctx context.Context, serviceName string) ([]*ServiceInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*ServiceInfo
	for _, svc := range r.services {
		if svc.Name == serviceName {
			result = append(result, svc)
		}
	}
	return result, nil
}

// GetAllServices 获取所有服务
func (r *StaticRegistry) GetAllServices() []*ServiceInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*ServiceInfo, 0, len(r.services))
	for _, svc := range r.services {
		result = append(result, svc)
	}
	return result
}

// ServiceRegistrar 服务注册器
// 封装注册逻辑，自动心跳
type ServiceRegistrar struct {
	registry Registry
	info    *ServiceInfo
	stopCh  chan struct{}
}

// NewServiceRegistrar 创建服务注册器
func NewServiceRegistrar(registry Registry, info *ServiceInfo) *ServiceRegistrar {
	return &ServiceRegistrar{
		registry: registry,
		info:    info,
		stopCh:  make(chan struct{}),
	}
}

// Start 启动注册器
func (s *ServiceRegistrar) Start(ctx context.Context) error {
	if err := s.registry.Register(ctx, s.info); err != nil {
		return fmt.Errorf("register failed: %w", err)
	}

	go s.heartbeatLoop(ctx)
	return nil
}

// heartbeatLoop 心跳循环
func (s *ServiceRegistrar) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			s.registry.Deregister(ctx, s.info.ID)
			return
		case <-ticker.C:
			if err := s.registry.Heartbeat(ctx, s.info.ID); err != nil {
				log.Printf("Heartbeat failed: %v", err)
			}
		}
	}
}

// Stop 停止注册器
func (s *ServiceRegistrar) Stop() {
	close(s.stopCh)
}
