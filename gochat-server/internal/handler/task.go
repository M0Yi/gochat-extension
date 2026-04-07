package handler

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/m0yi/gochat-server/internal/store"
	"github.com/m0yi/gochat-server/internal/types"
)

type TaskAPI struct {
	taskStore *store.TaskStore
	hub       *WSHub
}

func NewTaskAPI(ts *store.TaskStore, hub *WSHub) *TaskAPI {
	return &TaskAPI{taskStore: ts, hub: hub}
}

func (ta *TaskAPI) broadcastTasks(convID string) {
	tasks, err := ta.taskStore.ListTasks(convID)
	if err != nil {
		log.Printf("[task] broadcast query: %v", err)
		return
	}
	msg := types.WSMessage{
		Type:  "task_update",
		Title: "",
		Tasks: tasks,
	}
	if ta.hub != nil {
		ta.hub.Broadcast(msg)
	}
}

func (ta *TaskAPI) CreateTask(c *gin.Context) {
	convID := c.Param("conversationId")
	if convID == "" {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "conversationId required"})
		return
	}

	var req types.CreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid request: " + err.Error()})
		return
	}

	var task *types.Task
	var err error
	if req.Description != "" {
		task, err = ta.taskStore.CreateTaskWithDescription(convID, req.Title, req.Description)
	} else {
		task, err = ta.taskStore.CreateTask(convID, req.Title)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
		return
	}

	ta.broadcastTasks(convID)

	c.JSON(http.StatusCreated, task)
}

func (ta *TaskAPI) ListTasks(c *gin.Context) {
	convID := c.Param("conversationId")
	if convID == "" {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "conversationId required"})
		return
	}

	tasks, err := ta.taskStore.ListTasks(convID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
		return
	}

	c.JSON(http.StatusOK, types.TaskListResponse{Tasks: tasks})
}

func (ta *TaskAPI) ToggleTask(c *gin.Context) {
	taskID := c.Param("taskId")
	if taskID == "" {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "taskId required"})
		return
	}

	task, err := ta.taskStore.ToggleTask(taskID)
	if err != nil {
		c.JSON(http.StatusNotFound, types.ErrorResponse{Error: err.Error()})
		return
	}

	ta.broadcastTasks(task.ConversationID)

	c.JSON(http.StatusOK, task)
}

func (ta *TaskAPI) DeleteTask(c *gin.Context) {
	convID := c.Param("conversationId")
	taskID := c.Param("taskId")
	if taskID == "" {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "taskId required"})
		return
	}

	if err := ta.taskStore.DeleteTask(taskID); err != nil {
		c.JSON(http.StatusNotFound, types.ErrorResponse{Error: err.Error()})
		return
	}

	ta.broadcastTasks(convID)

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (ta *TaskAPI) ClearCompletedTasks(c *gin.Context) {
	convID := c.Param("conversationId")
	if convID == "" {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "conversationId required"})
		return
	}

	count, err := ta.taskStore.ClearCompleted(convID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
		return
	}

	ta.broadcastTasks(convID)

	c.JSON(http.StatusOK, gin.H{"ok": true, "cleared": count})
}

func (ta *TaskAPI) TaskSummary(c *gin.Context) {
	convID := c.Param("conversationId")
	if convID == "" {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "conversationId required"})
		return
	}

	summary, err := ta.taskStore.GetSummary(convID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
		return
	}

	c.JSON(http.StatusOK, summary)
}
