package cron

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// StatusPath is the sole read-only route mounted by Register.
const StatusPath = "/cron"

// Snapshot is the read-only service and durable-store projection returned by
// Snapshot and the Gin status route.
type Snapshot struct {
	GeneratedAt time.Time          `json:"generated_at"`
	Running     bool               `json:"running"`
	Jobs        []JobState         `json:"jobs"`
	Occurrences []OccurrenceRecord `json:"occurrences"`
}

// Snapshot returns a point-in-time read-only view of scheduler state.
func (service *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	stored, err := service.store.Snapshot(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		GeneratedAt: service.now().UTC(),
		Running:     service.isRunning(),
		Jobs:        stored.Jobs,
		Occurrences: stored.Occurrences,
	}, nil
}

// Register mounts the scheduler's read-only JSON status route. It never mounts
// mutation or run-now endpoints; callers choose the Gin group and therefore
// the authentication boundary.
func (service *Service) Register(router gin.IRouter) {
	router.GET(StatusPath, service.statusHandler)
}

func (service *Service) statusHandler(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	snapshot, err := service.Snapshot(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "cron status unavailable"})
		return
	}
	c.JSON(http.StatusOK, snapshot)
}
