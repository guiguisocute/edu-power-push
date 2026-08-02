package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
	"github.com/edu-power-push/edu-power-push/backend/internal/school"
)

/* 面板选学校接口。

   校名属于展示。选学校决定采集器目标上游。
   两者必须分离。禁止改文案时误切采集目标。 */

type schoolOptionResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Balance bool   `json:"balance"`
	Bill    bool   `json:"bill"`
	Detail  bool   `json:"detail"`
}

type schoolProviderResponse struct {
	Name           string                 `json:"name"`
	DisplayName    string                 `json:"display_name"`
	DefaultBaseURL string                 `json:"default_base_url"`
	Schools        []schoolOptionResponse `json:"schools"`
}

type schoolSettingsResponse struct {
	Providers []schoolProviderResponse `json:"providers"`
	Current   school.Binding           `json:"current"`
	// Source 为 "panel" 或 "env"。面板必须标明配置来源。
	Source string `json:"source"`
	// Configured 为 false 时采集器全部等待选学校。
	Configured bool    `json:"configured"`
	UpdatedAt  *string `json:"updated_at"`
	UpdatedBy  string  `json:"updated_by"`
}

func (s *Server) getSchoolSettings(w http.ResponseWriter, r *http.Request) {
	record, err := school.Load(r.Context(), s.pool, s.secrets, school.FromConfig(s.cfg))
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	response := schoolSettingsResponse{
		Providers:  make([]schoolProviderResponse, 0, len(provider.List())),
		Current:    record.Binding,
		Source:     record.Source,
		Configured: record.Binding.Configured(),
		UpdatedBy:  record.UpdatedBy,
	}
	if record.UpdatedAt != nil {
		formatted := record.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z")
		response.UpdatedAt = &formatted
	}
	for _, impl := range provider.List() {
		item := schoolProviderResponse{
			Name: impl.Name(), DisplayName: impl.DisplayName(),
			DefaultBaseURL: impl.DefaultBaseURL(),
			Schools:        []schoolOptionResponse{},
		}
		for _, option := range impl.Schools() {
			item.Schools = append(item.Schools, schoolOptionResponse{
				ID: option.ID, Name: option.Name,
				Balance: option.Balance, Bill: option.Bill, Detail: option.Detail,
			})
		}
		response.Providers = append(response.Providers, item)
	}
	s.writeJSON(w, http.StatusOK, response)
}

func (s *Server) putSchoolSettings(w http.ResponseWriter, r *http.Request) {
	var request school.Binding
	if !s.decodeJSON(w, r, &request) {
		return
	}
	request.Provider = strings.TrimSpace(request.Provider)
	request.AreaID = strings.TrimSpace(request.AreaID)
	request.AreaName = strings.TrimSpace(request.AreaName)
	request.BaseURL = strings.TrimSpace(request.BaseURL)

	if err := s.validateSchoolBinding(request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_school", err.Error())
		return
	}
	actor := actorFrom(r.Context())
	if _, err := school.Save(r.Context(), s.pool, s.secrets, request, actor.Label); err != nil {
		s.databaseError(w, r, err)
		return
	}
	/* 本进程立刻切换上游。worker 在下次轮询（15 秒内）跟上。
	   禁止等待 worker。保存请求不得被另一进程节奏拖住。 */
	if querier, cfg, err := school.Resolve(request, s.cfg.Scan.Timeout); err != nil {
		s.logger.Error("apply school binding", "error", err)
	} else if s.upstream != nil {
		s.upstream.Set(querier, cfg)
	}
	s.audit(r, "settings.school.save", "school", map[string]any{
		"provider": request.Provider, "area_id": request.AreaID, "area_name": request.AreaName,
	})
	s.getSchoolSettings(w, r)
}

/*
validateSchoolBinding 仅拦截必然失败的配置。

	清单是实测结果，不是白名单。清单外 area id 放行。
	面板提示未在实测清单中。能否连通由真实请求判定。
*/
func (s *Server) validateSchoolBinding(b school.Binding) error {
	if b.Provider != "" {
		if _, ok := provider.Get(b.Provider); !ok {
			return errors.New("unknown provider: " + b.Provider)
		}
	}
	if len(b.AreaID) > 64 {
		return errors.New("area_id must contain at most 64 characters")
	}
	if len(b.AreaName) > 120 {
		return errors.New("area_name must contain at most 120 characters")
	}
	if b.BaseURL != "" {
		parsed, err := url.Parse(b.BaseURL)
		if err != nil || parsed.Host == "" {
			return errors.New("base_url must be an absolute HTTP(S) URL")
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return errors.New("base_url scheme must be http or https")
		}
	}
	// 清空 area id 合法。含义是停止采集并准备换学校。
	if b.AreaID == "" {
		return nil
	}
	if _, _, err := school.Resolve(b, s.cfg.Scan.Timeout); err != nil {
		return err
	}
	return nil
}
