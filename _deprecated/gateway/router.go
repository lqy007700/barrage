package gateway

import (
	"sync"
)

// RoomRouter 房间路由表
// 维护 userId/roomId → EdgeNode 的映射
type RoomRouter struct {
	mu sync.RWMutex

	// userId → EdgeNodeId
	userToEdge map[int64]string

	// roomId → []EdgeNodeId (房间内的用户分布在哪些Edge)
	roomToEdges map[int64]map[string]struct{}

	// EdgeNodeId → EdgeInfo
	edges map[string]*EdgeInfo
}

// EdgeInfo Edge节点信息
type EdgeInfo struct {
	NodeId    string
	Addr      string
	ConnCount int32
}

// NewRoomRouter 创建路由表
func NewRoomRouter() *RoomRouter {
	return &RoomRouter{
		userToEdge: make(map[int64]string),
		roomToEdges: make(map[int64]map[string]struct{}),
		edges:       make(map[string]*EdgeInfo),
	}
}

// RegisterEdge 注册Edge节点
func (r *RoomRouter) RegisterEdge(nodeId, addr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.edges[nodeId] = &EdgeInfo{
		NodeId:    nodeId,
		Addr:      addr,
		ConnCount: 0,
	}
}

// UnregisterEdge 注销Edge节点
func (r *RoomRouter) UnregisterEdge(nodeId string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.edges, nodeId)
}

// BindUserToEdge 用户绑定到Edge
func (r *RoomRouter) BindUserToEdge(userId int64, nodeId string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.userToEdge[userId] = nodeId
}

// UnbindUser 解绑用户
func (r *RoomRouter) UnbindUser(userId int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if nodeId, ok := r.userToEdge[userId]; ok {
		delete(r.userToEdge, userId)
		// 从所有房间的Edge列表中移除
		for roomId, edges := range r.roomToEdges {
			delete(edges, nodeId)
			if len(edges) == 0 {
				delete(r.roomToEdges, roomId)
			}
		}
	}
}

// AddUserToRoom 用户加入房间
func (r *RoomRouter) AddUserToRoom(userId int64, roomId int64, nodeId string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.userToEdge[userId] = nodeId
	if r.roomToEdges[roomId] == nil {
		r.roomToEdges[roomId] = make(map[string]struct{})
	}
	r.roomToEdges[roomId][nodeId] = struct{}{}
}

// RemoveUserFromRoom 用户离开房间
func (r *RoomRouter) RemoveUserFromRoom(userId int64, roomId int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	nodeId := r.userToEdge[userId]
	delete(r.userToEdge, userId)
	if nodeId != "" && r.roomToEdges[roomId] != nil {
		delete(r.roomToEdges[roomId], nodeId)
		if len(r.roomToEdges[roomId]) == 0 {
			delete(r.roomToEdges, roomId)
		}
	}
}

// GetUserEdge 获取用户所在的Edge
func (r *RoomRouter) GetUserEdge(userId int64) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	edge, ok := r.userToEdge[userId]
	return edge, ok
}

// GetRoomEdges 获取房间所在的所有Edge
func (r *RoomRouter) GetRoomEdges(roomId int64) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	edges, ok := r.roomToEdges[roomId]
	if !ok {
		return nil
	}
	result := make([]string, 0, len(edges))
	for edge := range edges {
		result = append(result, edge)
	}
	return result
}

// GetEdge 获取Edge信息
func (r *RoomRouter) GetEdge(nodeId string) (*EdgeInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.edges[nodeId]
	return info, ok
}

// IncEdgeConnCount 增加Edge连接数
func (r *RoomRouter) IncEdgeConnCount(nodeId string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if info, ok := r.edges[nodeId]; ok {
		info.ConnCount++
	}
}

// DecEdgeConnCount 减少Edge连接数
func (r *RoomRouter) DecEdgeConnCount(nodeId string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if info, ok := r.edges[nodeId]; ok && info.ConnCount > 0 {
		info.ConnCount--
	}
}

// GetAllEdges 获取所有Edge
func (r *RoomRouter) GetAllEdges() []*EdgeInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*EdgeInfo, 0, len(r.edges))
	for _, info := range r.edges {
		result = append(result, info)
	}
	return result
}

// SelectEdgeForUser 为用户选择Edge (简单轮询)
func (r *RoomRouter) SelectEdgeForUser(userId int64) (string, string, bool) {
	r.mu.RLock()
	edges := r.GetAllEdgesLocked()
	r.mu.RUnlock()
	if len(edges) == 0 {
		return "", "", false
	}
	idx := int(userId) % len(edges)
	info := edges[idx]
	return info.NodeId, info.Addr, true
}

// GetAllEdgesLocked 获取所有Edge (内部使用,已加锁)
func (r *RoomRouter) GetAllEdgesLocked() []*EdgeInfo {
	result := make([]*EdgeInfo, 0, len(r.edges))
	for _, info := range r.edges {
		result = append(result, info)
	}
	return result
}
