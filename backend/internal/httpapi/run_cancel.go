package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

/* 三类采集任务的取消入口。

   API 进程无法直接停止 worker 循环。此处仅写请求取消标记。
   worker 心跳（15 秒一次）读取后停止派发并把 run 收成 canceled。
   响应使用 202：已接受请求，终止随后发生。 */

// cancelRun 统一三类采集器取消流程：写标记，然后回读当前视图。
func cancelRun[T any](
	s *Server,
	w http.ResponseWriter,
	r *http.Request,
	kind string,
	request func(context.Context, *pgxpool.Pool, string) error,
	get func(context.Context, *pgxpool.Pool, string) (T, error),
) {
	runID := r.PathValue("run_id")
	err := request(r.Context(), s.pool, runID)
	switch {
	case errors.Is(err, storage.ErrRunNotCancelable):
		// 已收尾与不存在的 run 此处无法区分。回读一次以返回准确状态码。
		if _, getErr := get(r.Context(), s.pool, runID); errors.Is(getErr, pgx.ErrNoRows) {
			s.writeError(w, r, http.StatusNotFound, "not_found", kind+" run not found")
			return
		}
		s.writeError(w, r, http.StatusConflict, "run_not_cancelable", "this run has already finished")
		return
	case err != nil:
		s.databaseError(w, r, err)
		return
	}
	s.audit(r, kind+"_run.cancel", runID, nil)
	item, err := get(r.Context(), s.pool, runID)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusAccepted, item)
}

func (s *Server) cancelScanRun(w http.ResponseWriter, r *http.Request) {
	cancelRun(s, w, r, "scan", storage.RequestScanRunCancel, storage.GetScanRun)
}

func (s *Server) cancelBillRun(w http.ResponseWriter, r *http.Request) {
	cancelRun(s, w, r, "bill", storage.RequestBillRunCancel, storage.GetBillRun)
}

func (s *Server) cancelDailyDetailRun(w http.ResponseWriter, r *http.Request) {
	cancelRun(s, w, r, "daily_detail", storage.RequestDailyDetailRunCancel, storage.GetDailyDetailRun)
}
