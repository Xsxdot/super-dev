// handler_ingress.go 实现 Ingress 子系统的 HTTP 处理器。
//
// 职责：
//   - 暴露入口声明 CRUD、预览、应用和孤儿资源确认删除接口
//   - 暴露 DNS provider 配置的保存、列表和删除接口
//   - 将 HTTP 请求解析为 ingress.Service 所需的输入
//
// 边界：
//   - 不在 handler 内编排 DNS、证书或 proxy 收敛逻辑
//   - 不向响应返回 DNS provider secrets
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/superdev/agent/ingress"
	"github.com/superdev/agent/model"
)

type ingressApplyRequest struct {
	ConfirmedDNSValue string `json:"confirmed_dns_value"`
}

type ingressOrphanRemovalRequest struct {
	Configs []ingress.OrphanConfig `json:"configs"`
	Records []ingress.Record       `json:"records"`
}

func (a *App) listIngress(w http.ResponseWriter, r *http.Request) {
	list, err := a.ingressStore.ListIngress()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, list)
}

func (a *App) listProjectIngress(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	list, err := a.ingressStore.ListIngressByProject(projectID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, list)
}

func (a *App) createProjectIngress(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	var in ingress.Ingress
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in.ProjectID = projectID
	saved, err := a.ingressStore.UpsertIngress(in)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, saved)
}

func (a *App) upsertIngress(w http.ResponseWriter, r *http.Request) {
	var in ingress.Ingress
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	saved, err := a.ingressStore.UpsertIngress(in)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, saved)
}

func (a *App) getProjectIngress(w http.ResponseWriter, r *http.Request) {
	in, ok := a.projectIngressForRequest(w, r)
	if !ok {
		return
	}
	jsonOK(w, in)
}

func (a *App) getIngress(w http.ResponseWriter, r *http.Request) {
	in, ok, err := a.ingressStore.GetIngress(r.PathValue("id"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		jsonError(w, http.StatusNotFound, "ingress not found")
		return
	}
	jsonOK(w, in)
}

func (a *App) updateProjectIngress(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	ingressID := r.PathValue("ingressID")
	if _, ok := a.projectIngressForRequest(w, r); !ok {
		return
	}

	var in ingress.Ingress
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in.ID = ingressID
	in.ProjectID = projectID
	saved, err := a.ingressStore.UpsertIngress(in)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, saved)
}

func (a *App) updateIngress(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok, err := a.ingressStore.GetIngress(id); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !ok {
		jsonError(w, http.StatusNotFound, "ingress not found")
		return
	}

	var in ingress.Ingress
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in.ID = id
	saved, err := a.ingressStore.UpsertIngress(in)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, saved)
}

func (a *App) deleteProjectIngress(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.projectIngressForRequest(w, r); !ok {
		return
	}
	if err := a.ingressStore.DeleteIngress(r.PathValue("ingressID")); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}

func (a *App) deleteIngress(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok, err := a.ingressStore.GetIngress(id); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !ok {
		jsonError(w, http.StatusNotFound, "ingress not found")
		return
	}
	if err := a.ingressStore.DeleteIngress(id); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}

func (a *App) previewProjectIngress(w http.ResponseWriter, r *http.Request) {
	in, ok := a.projectIngressForRequest(w, r)
	if !ok {
		return
	}
	result, err := a.ingressService.Preview(r.Context(), in)
	if err != nil {
		jsonError(w, ingressErrorStatus(err), err.Error())
		return
	}
	jsonOK(w, result)
}

func (a *App) previewIngress(w http.ResponseWriter, r *http.Request) {
	in, ok, err := a.ingressStore.GetIngress(r.PathValue("id"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		jsonError(w, http.StatusNotFound, "ingress not found")
		return
	}
	result, err := a.ingressService.Preview(r.Context(), in)
	if err != nil {
		jsonError(w, ingressErrorStatus(err), err.Error())
		return
	}
	jsonOK(w, result)
}

func (a *App) applyProjectIngress(w http.ResponseWriter, r *http.Request) {
	var req ingressApplyRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if _, ok := a.projectIngressForRequest(w, r); !ok {
		return
	}
	state, err := a.ingressService.Apply(r.Context(), r.PathValue("ingressID"), ingress.ApplyOptions{
		ConfirmedDNSValue: req.ConfirmedDNSValue,
	})
	if err != nil {
		jsonError(w, ingressErrorStatus(err), err.Error())
		return
	}
	jsonOK(w, state)
}

func (a *App) applyIngress(w http.ResponseWriter, r *http.Request) {
	var req ingressApplyRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	state, err := a.ingressService.Apply(r.Context(), r.PathValue("id"), ingress.ApplyOptions{
		ConfirmedDNSValue: req.ConfirmedDNSValue,
	})
	if err != nil {
		jsonError(w, ingressErrorStatus(err), err.Error())
		return
	}
	jsonOK(w, state)
}

func (a *App) detectProjectIngressOrphans(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.projectIngressForRequest(w, r); !ok {
		return
	}
	report, err := a.ingressService.DetectOrphans(r.Context(), r.PathValue("ingressID"))
	if err != nil {
		jsonError(w, ingressErrorStatus(err), err.Error())
		return
	}
	jsonOK(w, report)
}

func (a *App) detectIngressOrphans(w http.ResponseWriter, r *http.Request) {
	report, err := a.ingressService.DetectOrphans(r.Context(), r.PathValue("id"))
	if err != nil {
		jsonError(w, ingressErrorStatus(err), err.Error())
		return
	}
	jsonOK(w, report)
}

func (a *App) removeProjectIngressOrphans(w http.ResponseWriter, r *http.Request) {
	var req ingressOrphanRemovalRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if _, ok := a.projectIngressForRequest(w, r); !ok {
		return
	}
	report := ingress.OrphanReport{Configs: req.Configs, Records: req.Records}
	if err := a.ingressService.RemoveOrphans(r.Context(), r.PathValue("ingressID"), report); err != nil {
		jsonError(w, ingressErrorStatus(err), err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}

func (a *App) removeIngressOrphans(w http.ResponseWriter, r *http.Request) {
	var req ingressOrphanRemovalRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	report := ingress.OrphanReport{Configs: req.Configs, Records: req.Records}
	if err := a.ingressService.RemoveOrphans(r.Context(), r.PathValue("id"), report); err != nil {
		jsonError(w, ingressErrorStatus(err), err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}

func (a *App) inferProjectIngressDefaults(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	project, ok := a.projectForIngressRequest(projectID)
	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}
	var req ingress.InferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	hosts, err := a.ingressInferenceHosts()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	result, err := ingress.InferDefaults(project, hosts, req)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOK(w, result)
}

func (a *App) projectIngressForRequest(w http.ResponseWriter, r *http.Request) (ingress.Ingress, bool) {
	projectID := r.PathValue("id")
	in, ok, err := a.ingressStore.GetIngress(r.PathValue("ingressID"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return ingress.Ingress{}, false
	}
	if !ok {
		jsonError(w, http.StatusNotFound, "ingress not found")
		return ingress.Ingress{}, false
	}
	if in.ProjectID != projectID {
		jsonError(w, http.StatusNotFound, "ingress not found")
		return ingress.Ingress{}, false
	}
	return in, true
}

func (a *App) projectForIngressRequest(projectID string) (model.Project, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.findProject(projectID)
}

func (a *App) ingressInferenceHosts() ([]model.Host, error) {
	hosts, err := a.remoteStore.ListHosts()
	if err != nil {
		return nil, err
	}
	return append([]model.Host{{
		ID:      a.identity.NodeID,
		Name:    a.identity.DisplayName,
		SSHHost: "127.0.0.1",
	}}, hosts...), nil
}

func (a *App) listIngressDNSProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := a.ingressStore.ListDNSProviders()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, providers)
}

func (a *App) upsertIngressDNSProvider(w http.ResponseWriter, r *http.Request) {
	var cfg ingress.DNSProviderConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateIngressDNSProviderConfig(cfg); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg = normalizeIngressDNSProviderConfig(cfg)
	saved, err := a.ingressStore.UpsertDNSProvider(cfg)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := a.registerIngressDNSProvider(saved); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOK(w, redactDNSProviderConfig(saved))
}

func (a *App) deleteIngressDNSProvider(w http.ResponseWriter, r *http.Request) {
	if err := a.ingressStore.DeleteDNSProvider(r.PathValue("id")); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}

func normalizeIngressDNSProviderConfig(cfg ingress.DNSProviderConfig) ingress.DNSProviderConfig {
	cfg.Type = strings.ToLower(strings.TrimSpace(cfg.Type))
	if cfg.Type == "aliyun" {
		cfg.ZoneID = ""
	}
	return cfg
}

func validateIngressDNSProviderConfig(cfg ingress.DNSProviderConfig) error {
	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "cloudflare", "aliyun":
		return nil
	case "manual":
		return errors.New("manual DNS provider is built in")
	default:
		return errors.New("unsupported DNS provider type")
	}
}

func redactDNSProviderConfig(cfg ingress.DNSProviderConfig) ingress.DNSProviderConfig {
	cfg.Secrets = nil
	return cfg
}

func ingressErrorStatus(err error) int {
	if errors.Is(err, ingress.ErrProviderNotFound) {
		return http.StatusNotFound
	}
	msg := err.Error()
	if strings.Contains(msg, "not found") {
		return http.StatusNotFound
	}
	if strings.Contains(msg, "is required") ||
		strings.Contains(msg, "cannot") ||
		strings.Contains(msg, "must match") ||
		strings.Contains(msg, "not ready") ||
		strings.Contains(msg, "certificate") ||
		strings.Contains(msg, "证书") ||
		strings.Contains(msg, "acme account email is required") ||
		strings.Contains(msg, "unsupported") {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}
