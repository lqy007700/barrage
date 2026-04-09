package registry

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/hashicorp/consul/api"
	"go.etcd.io/etcd/client/v3"
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

// ConsulRegistry Consul实现
type ConsulRegistry struct {
	addr   string
	client *api.Client
}

// NewConsulRegistry 创建Consul注册中心
func NewConsulRegistry(addr string) (*ConsulRegistry, error) {
	config := &api.Config{
		Address: addr,
	}
	client, err := api.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("create consul client: %w", err)
	}

	log.Printf("[Consul] Registry connected to %s", addr)
	return &ConsulRegistry{
		addr:   addr,
		client: client,
	}, nil
}

// Register 注册服务
func (r *ConsulRegistry) Register(ctx context.Context, info *ServiceInfo) error {
	registration := &api.AgentServiceRegistration{
		ID:      info.ID,
		Name:    info.Name,
		Address: info.Addr,
		Port:    info.Port,
		Tags:    info.Tags,
		Meta:    info.Meta,
	}

	// 设置健康检查
	registration.Check = &api.AgentServiceCheck{
		HTTP:                           fmt.Sprintf("http://%s:%d/health", info.Addr, info.Port),
		Interval:                       "10s",
		Timeout:                        "5s",
		DeregisterCriticalServiceAfter: "30s",
	}

	if err := r.client.Agent().ServiceRegister(registration); err != nil {
		return fmt.Errorf("register service to consul: %w", err)
	}

	log.Printf("[Consul] Registered service: %s -> %s:%d", info.Name, info.Addr, info.Port)
	return nil
}

// Deregister 注销服务
func (r *ConsulRegistry) Deregister(ctx context.Context, serviceId string) error {
	if err := r.client.Agent().ServiceDeregister(serviceId); err != nil {
		return fmt.Errorf("deregister service from consul: %w", err)
	}
	log.Printf("[Consul] Deregistered service: %s", serviceId)
	return nil
}

// Heartbeat 心跳
// Consul 使用 AgentServiceCheck 自动进行 HTTP 健康检查，无需手动心跳
// 此方法保留为接口实现，但实际不会被调用（heartbeatLoop 中已过滤非 Etcd 类型）
func (r *ConsulRegistry) Heartbeat(ctx context.Context, serviceId string) error {
	return nil
}

// GetServices 获取服务列表
func (r *ConsulRegistry) GetServices(ctx context.Context, serviceName string) ([]*ServiceInfo, error) {
	services, err := r.client.Agent().ServicesWithFilter(fmt.Sprintf(`Service == "%s"`, serviceName))
	if err != nil {
		return nil, fmt.Errorf("query services from consul: %w", err)
	}

	var result []*ServiceInfo
	for _, svc := range services {
		result = append(result, &ServiceInfo{
			ID:   svc.ID,
			Name: svc.Service,
			Addr: svc.Address,
			Port: svc.Port,
			Tags: svc.Tags,
			Meta: svc.Meta,
		})
	}

	return result, nil
}

// GetService 获取单个服务
func (r *ConsulRegistry) GetService(ctx context.Context, serviceId string) (*ServiceInfo, error) {
	svc, _, err := r.client.Agent().Service(serviceId, nil)
	if err != nil {
		return nil, fmt.Errorf("get service from consul: %w", err)
	}

	return &ServiceInfo{
		ID:   svc.ID,
		Name: svc.Service,
		Addr: svc.Address,
		Port: svc.Port,
		Tags: svc.Tags,
		Meta: svc.Meta,
	}, nil
}

// EtcdRegistry Etcd实现
type EtcdRegistry struct {
	addr   string
	client *clientv3.Client
}

// NewEtcdRegistry 创建Etcd注册中心
func NewEtcdRegistry(addr string) (*EtcdRegistry, error) {
	client, err := clientv3.NewFromURL(addr)
	if err != nil {
		return nil, fmt.Errorf("create etcd client: %w", err)
	}

	log.Printf("[Etcd] Registry connected to %s", addr)
	return &EtcdRegistry{
		addr:   addr,
		client: client,
	}, nil
}

// Close 关闭Etcd客户端
func (r *EtcdRegistry) Close() error {
	return r.client.Close()
}

// serviceKey 生成服务在etcd中的key
func serviceKey(serviceId string) string {
	return fmt.Sprintf("/services/%s", serviceId)
}

// Register 注册服务
func (r *EtcdRegistry) Register(ctx context.Context, info *ServiceInfo) error {
	// 将服务信息存入etcd，使用serviceId作为key
	key := serviceKey(info.ID)

	// 构建value
	value := fmt.Sprintf("%s:%d", info.Addr, info.Port)

	// 设置10秒的TTL，Heartbeat会续约
	lease, err := r.client.Grant(ctx, 10)
	if err != nil {
		return fmt.Errorf("create etcd lease: %w", err)
	}

	_, err = r.client.Put(ctx, key, value, clientv3.WithLease(lease.ID))
	if err != nil {
		return fmt.Errorf("put service to etcd: %w", err)
	}

	// 存储完整的元数据
	metaKey := fmt.Sprintf("%s/meta", key)
	_, err = r.client.Put(ctx, metaKey, fmt.Sprintf(`{"name":"%s","tags":%v,"meta":%v}`,
		info.Name, info.Tags, info.Meta), clientv3.WithLease(lease.ID))
	if err != nil {
		return fmt.Errorf("put service meta to etcd: %w", err)
	}

	log.Printf("[Etcd] Registered service: %s -> %s", info.Name, value)
	return nil
}

// Deregister 注销服务
func (r *EtcdRegistry) Deregister(ctx context.Context, serviceId string) error {
	key := serviceKey(serviceId)
	_, err := r.client.Delete(ctx, key)
	if err != nil {
		return fmt.Errorf("delete service from etcd: %w", err)
	}

	// 删除元数据
	metaKey := fmt.Sprintf("%s/meta", key)
	r.client.Delete(ctx, metaKey)

	log.Printf("[Etcd] Deregistered service: %s", serviceId)
	return nil
}

// Heartbeat 心跳 (续约TTL)
func (r *EtcdRegistry) Heartbeat(ctx context.Context, serviceId string) error {
	key := serviceKey(serviceId)

	// 获取现有的key
	resp, err := r.client.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("get service from etcd: %w", err)
	}

	if resp.Count == 0 {
		return fmt.Errorf("service not found: %s", serviceId)
	}

	// 创建一个新的10秒TTL的lease来续约
	lease, err := r.client.Grant(ctx, 10)
	if err != nil {
		return fmt.Errorf("create etcd lease: %w", err)
	}

	// 重新put以续约TTL
	_, err = r.client.Put(ctx, key, string(resp.Kvs[0].Value), clientv3.WithLease(lease.ID))
	if err != nil {
		return fmt.Errorf("refresh service TTL in etcd: %w", err)
	}

	// 同样续约元数据
	metaKey := fmt.Sprintf("%s/meta", key)
	metaResp, _ := r.client.Get(ctx, metaKey)
	if metaResp != nil && metaResp.Count > 0 {
		_, _ = r.client.Put(ctx, metaKey, string(metaResp.Kvs[0].Value), clientv3.WithLease(lease.ID))
	}

	return nil
}

// GetServices 获取服务列表
func (r *EtcdRegistry) GetServices(ctx context.Context, serviceName string) ([]*ServiceInfo, error) {
	// 查询所有services下的key
	resp, err := r.client.Get(ctx, "/services/", clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("list services from etcd: %w", err)
	}

	var result []*ServiceInfo
	for _, kv := range resp.Kvs {
		key := string(kv.Key)
		// 跳过meta key
		if len(key) > 5 && key[len(key)-5:] == "/meta" {
			continue
		}

		// 提取serviceId从key中: /services/{serviceId}
		serviceId := key[10:] // 去掉 "/services/" 前缀

		// 获取元数据
		metaKey := fmt.Sprintf("%s/meta", key)
		metaResp, _ := r.client.Get(ctx, metaKey)

		info := &ServiceInfo{
			ID:   serviceId,
			Addr: string(kv.Value),
		}

		// 解析 addr:port 格式
		fmt.Sscanf(info.Addr, "%s:%d", &info.Addr, &info.Port)

		if metaResp != nil && metaResp.Count > 0 {
			// 元数据可以进一步解析，这里简化处理
			info.Name = serviceName
		}

		result = append(result, info)
	}

	return result, nil
}

// NewRegistry 根据类型创建注册中心
// 类型支持: "static", "consul", "etcd"
func NewRegistry(registryType string, addr string) (Registry, error) {
	switch registryType {
	case "consul":
		return NewConsulRegistry(addr)
	case "etcd":
		return NewEtcdRegistry(addr)
	case "static":
		return NewStaticRegistry(), nil
	default:
		return nil, fmt.Errorf("unknown registry type: %s", registryType)
	}
}

// ServiceRegistrar 服务注册器
// 封装注册逻辑，自动心跳
type ServiceRegistrar struct {
	registry Registry
	info     *ServiceInfo
	stopCh   chan struct{}
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
// 注意：Consul 使用 HTTP 健康检查自动进行心跳，无需手动续约
// Etcd 使用 TTL 续约机制，需要此心跳循环来维持服务注册
// 为保持代码兼容性，此处保留心跳逻辑，实际是否执行由 Registry 类型决定
// func (s *ServiceRegistrar) heartbeatLoop(ctx context.Context) {
func (s *ServiceRegistrar) heartbeatLoop(ctx context.Context) {
	// Consul Registry 不需要心跳，Consul Agent 会自动进行健康检查
	// 只有 Etcd Registry 才需要 TTL 续约
	if _, ok := s.registry.(*EtcdRegistry); !ok {
		return
	}

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
