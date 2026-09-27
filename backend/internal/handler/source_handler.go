package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/sources"
	"github.com/labstack/echo/v4"
)

type SourceFetcher interface {
	Fetch(context.Context, string) (*sources.Document, error)
}

type SourceHandler struct{ fetcher SourceFetcher }

func NewSourceHandler(fetcher SourceFetcher) *SourceHandler {
	if fetcher == nil {
		fetcher = sources.NewFetcher()
	}
	return &SourceHandler{fetcher: fetcher}
}

// HandlePreview returns extracted content only. It does not create a problem,
// invoke a model, crawl collection members or download judge data.
func (h *SourceHandler) HandlePreview(c echo.Context) error {
	if h == nil || h.fetcher == nil {
		return serviceUnavailable(c, "source preview is unavailable")
	}
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, 16<<10)
	var request struct {
		URL string `json:"url"`
	}
	if err := c.Bind(&request); err != nil {
		return badRequest(c, "INVALID_BODY", "请提供有效的网页链接 JSON。")
	}
	if strings.TrimSpace(request.URL) == "" {
		return badRequest(c, "SOURCE_INVALID_URL", "请输入公开网页链接；也可以粘贴题面继续。")
	}
	document, err := h.fetcher.Fetch(c.Request().Context(), request.URL)
	if err != nil {
		var sourceErr *sources.Error
		if errors.As(err, &sourceErr) {
			return badRequest(c, sourceErr.Code, sourceErr.Message)
		}
		return badRequest(c, "SOURCE_FETCH_FAILED", "无法获取来源内容；也可以粘贴题面继续。")
	}
	return ok(c, document)
}
