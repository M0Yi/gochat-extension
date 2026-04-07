package handler

import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/m0yi/gochat-server/internal/store"
	"github.com/m0yi/gochat-server/internal/types"
)

const pluginExpireTimeout = 60 * time.Second

type PluginTracker struct {
	mu      sync.Mutex
	plugins map[string]*types.PluginInfo
}

func NewPluginTracker() *PluginTracker {
	pt := &PluginTracker{
		plugins: make(map[string]*types.PluginInfo),
	}
	go pt.cleanupLoop()
	return pt
}

func (pt *PluginTracker) Register(pluginID, name, remoteAddr string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	now := time.Now()
	if p, ok := pt.plugins[pluginID]; ok {
		p.RemoteAddr = remoteAddr
		p.LastSeen = now
		return
	}

	pt.plugins[pluginID] = &types.PluginInfo{
		PluginID:    pluginID,
		Name:        name,
		RemoteAddr:  remoteAddr,
		ConnectedAt: now,
		LastSeen:    now,
	}
	log.Printf("[plugin] registered: %s (%s) from %s", pluginID, name, remoteAddr)
}

func (pt *PluginTracker) ListPlugins() []types.PluginInfo {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	now := time.Now()
	result := make([]types.PluginInfo, 0)
	for _, p := range pt.plugins {
		if now.Sub(p.LastSeen) < pluginExpireTimeout {
			result = append(result, *p)
		}
	}
	return result
}

func (pt *PluginTracker) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		pt.mu.Lock()
		now := time.Now()
		for id, p := range pt.plugins {
			if now.Sub(p.LastSeen) >= pluginExpireTimeout {
				log.Printf("[plugin] expired: %s (%s)", id, p.Name)
				delete(pt.plugins, id)
			}
		}
		pt.mu.Unlock()
	}
}

func HandlePluginRegister(channelStore *store.ChannelStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Name string `json:"name"`
		}
		_ = c.ShouldBindJSON(&req)
		if req.Name == "" {
			req.Name = "My OpenClaw"
		}

		channel, err := channelStore.CreateChannel(req.Name, "")
		if err != nil {
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"channelId": channel.ID,
			"secret":    channel.Secret,
			"name":      channel.Name,
		})
	}
}

func HandlePluginHeartbeat(pt *PluginTracker, hub *WSHub) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			PluginID string `json:"pluginId"`
			Name     string `json:"name"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid request"})
			return
		}

		if req.PluginID == "" {
			req.PluginID = "default"
		}
		if req.Name == "" {
			req.Name = "OpenClaw Plugin"
		}

		remoteAddr := c.ClientIP()
		pt.Register(req.PluginID, req.Name, remoteAddr)

		hub.Broadcast(types.WSMessage{
			Type: "plugin_update",
		})

		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}
