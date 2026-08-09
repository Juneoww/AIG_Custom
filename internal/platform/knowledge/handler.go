package knowledge

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/gin-gonic/gin"
)

var errLegacyMutationRejected = errors.New("旧知识库处理器拒绝了变更")

const governedMutationContextKey = "platform_knowledge_governed_mutation"

const asyncCompletionContextKey = "platform_knowledge_async_completion"

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// Govern converts an existing format-preserving Gin mutation handler into a
// platform-controlled, administrator-only and audited facade.
func (handler *Handler) Govern(kind Kind, operation Operation, next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		subject, ok := identity.CurrentSubject(c)
		if !ok {
			c.Status(http.StatusUnauthorized)
			return
		}
		change := Change{Kind: kind, Operation: operation, ResourceID: resourceID(c)}
		originalWriter := c.Writer
		bufferedWriter := newBufferedResponseWriter(originalWriter)
		c.Writer = bufferedWriter
		err := handler.service.Apply(c.Request.Context(), subject, change, func() error {
			c.Set(governedMutationContextKey, true)
			next(c)
			if c.IsAborted() || c.Writer.Status() >= http.StatusBadRequest {
				return errLegacyMutationRejected
			}
			return nil
		})
		c.Writer = originalWriter
		switch {
		case errors.Is(err, ErrForbidden):
			if !c.Writer.Written() {
				c.Status(http.StatusForbidden)
			}
		case errors.Is(err, errLegacyMutationRejected):
			bufferedWriter.commit()
			return
		case err != nil:
			c.Status(http.StatusInternalServerError)
		default:
			bufferedWriter.commit()
		}
	}
}

// GovernAsync records a durable request before starting an asynchronous
// knowledge mutation. The handler retrieves CurrentAsyncCompletion and calls
// it only when the background operation actually succeeds or fails.
func (handler *Handler) GovernAsync(kind Kind, operation Operation, next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		subject, ok := identity.CurrentSubject(c)
		if !ok {
			c.Status(http.StatusUnauthorized)
			return
		}
		change := Change{Kind: kind, Operation: operation, ResourceID: resourceID(c)}
		completion, err := handler.service.BeginAsync(c.Request.Context(), subject, change)
		if errors.Is(err, ErrForbidden) {
			c.Status(http.StatusForbidden)
			return
		}
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		var completionMu sync.Mutex
		completed := false
		wrappedCompletion := AsyncCompletion(func(completionContext context.Context, success bool, metadata map[string]any) error {
			completionMu.Lock()
			if completed {
				completionMu.Unlock()
				return nil
			}
			completed = true
			completionMu.Unlock()
			return completion(completionContext, success, metadata)
		})
		c.Set(governedMutationContextKey, true)
		c.Set(asyncCompletionContextKey, wrappedCompletion)
		next(c)
		if c.IsAborted() || c.Writer.Status() >= http.StatusBadRequest {
			_ = wrappedCompletion(context.WithoutCancel(c.Request.Context()), false, map[string]any{"reason": "request_rejected"})
		}
	}
}

func CurrentAsyncCompletion(c *gin.Context) (AsyncCompletion, bool) {
	value, exists := c.Get(asyncCompletionContextKey)
	completion, ok := value.(AsyncCompletion)
	return completion, exists && ok
}

func IsGovernedMutation(c *gin.Context) bool {
	value, exists := c.Get(governedMutationContextKey)
	governed, ok := value.(bool)
	return exists && ok && governed
}

func resourceID(c *gin.Context) string {
	for _, parameter := range []string{"name", "cve", "id"} {
		if value := c.Param(parameter); value != "" {
			return value
		}
	}
	if c.FullPath() != "" {
		return c.FullPath()
	}
	return c.Request.URL.Path
}

type bufferedResponseWriter struct {
	gin.ResponseWriter
	header      http.Header
	body        bytes.Buffer
	status      int
	wroteHeader bool
}

func newBufferedResponseWriter(writer gin.ResponseWriter) *bufferedResponseWriter {
	return &bufferedResponseWriter{ResponseWriter: writer, header: writer.Header().Clone(), status: http.StatusOK}
}

func (writer *bufferedResponseWriter) Header() http.Header { return writer.header }

func (writer *bufferedResponseWriter) WriteHeader(status int) {
	if writer.wroteHeader {
		return
	}
	writer.status = status
	writer.wroteHeader = true
}

func (writer *bufferedResponseWriter) Write(data []byte) (int, error) {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.body.Write(data)
}

func (writer *bufferedResponseWriter) WriteString(data string) (int, error) {
	return writer.Write([]byte(data))
}

func (writer *bufferedResponseWriter) WriteHeaderNow() {
	if !writer.wroteHeader {
		writer.WriteHeader(writer.status)
	}
}

func (writer *bufferedResponseWriter) Status() int { return writer.status }

func (writer *bufferedResponseWriter) Size() int {
	if !writer.Written() {
		return -1
	}
	return writer.body.Len()
}

func (writer *bufferedResponseWriter) Written() bool {
	return writer.wroteHeader || writer.body.Len() > 0
}

func (writer *bufferedResponseWriter) Flush() { writer.WriteHeaderNow() }

func (writer *bufferedResponseWriter) commit() {
	target := writer.ResponseWriter
	for key := range target.Header() {
		delete(target.Header(), key)
	}
	for key, values := range writer.header {
		target.Header()[key] = append([]string(nil), values...)
	}
	if !writer.Written() {
		return
	}
	target.WriteHeader(writer.status)
	_, _ = target.Write(writer.body.Bytes())
}
