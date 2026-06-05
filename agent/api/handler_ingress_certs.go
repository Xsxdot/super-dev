// handler_ingress_certs.go 实现全局 SSL 证书和 ACME 账号 HTTP 处理器。
//
// 职责：
//   - 暴露托管证书 CRUD、申请、续期、部署和匹配接口
//   - 暴露全局 ACME 账号读写接口
//   - 确保 HTTP 响应不返回私钥
//
// 边界：
//   - 不在 handler 内实现 ACME 或远端部署细节
//   - 不管理项目级 Ingress 声明
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/xsxdot/super-dev/agent/ingress"
)

type certificateCreateRequest struct {
	Domains     []string                  `json:"domains"`
	Issuer      ingress.CertificateIssuer `json:"issuer"`
	DNSProvider string                    `json:"dns_provider"`
	AutoRenew   bool                      `json:"auto_renew"`
	Material    *ingress.Certificate      `json:"material"`
}

type certificateDeployRequest struct {
	HostIDs           []string                         `json:"host_ids,omitempty"`
	CertPath          string                           `json:"cert_path,omitempty"`
	KeyPath           string                           `json:"key_path,omitempty"`
	PostDeployCommand string                           `json:"post_deploy_command,omitempty"`
	Deployments       []certificateDeployTargetRequest `json:"deployments,omitempty"`
}

type certificateDeployTargetRequest struct {
	HostID            string `json:"host_id"`
	CertPath          string `json:"cert_path,omitempty"`
	KeyPath           string `json:"key_path,omitempty"`
	PostDeployCommand string `json:"post_deploy_command,omitempty"`
}

func (a *App) listIngressCertificates(w http.ResponseWriter, r *http.Request) {
	certs, err := a.ingressStore.ListCertificates()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, certs)
}

func (a *App) createIngressCertificate(w http.ResponseWriter, r *http.Request) {
	var req certificateCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cert := ingress.ManagedCertificate{
		Domains:     normalizeStringList(req.Domains),
		Issuer:      req.Issuer,
		DNSProvider: strings.TrimSpace(req.DNSProvider),
		Status:      ingress.CertPending,
		Material:    req.Material,
		AutoRenew:   req.AutoRenew,
	}
	if len(cert.Domains) == 0 {
		jsonError(w, http.StatusBadRequest, "domains is required")
		return
	}
	if cert.Issuer == "" {
		cert.Issuer = ingress.CertificateIssuerACME
	}
	if cert.Issuer == ingress.CertificateIssuerManual {
		if cert.Material == nil || strings.TrimSpace(cert.Material.CertPEM) == "" || strings.TrimSpace(cert.Material.KeyPEM) == "" {
			jsonError(w, http.StatusBadRequest, "manual certificate material is required")
			return
		}
		cert.Status = ingress.CertActive
		if strings.TrimSpace(cert.Material.Domain) == "" {
			cert.Material.Domain = cert.Domains[0]
		}
		if strings.TrimSpace(cert.Material.Provider) == "" {
			cert.Material.Provider = string(ingress.CertificateIssuerManual)
		}
	}
	saved, err := a.ingressStore.UpsertCertificate(cert)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	redactManagedCertificate(&saved)
	jsonOK(w, saved)
}

func (a *App) getIngressCertificate(w http.ResponseWriter, r *http.Request) {
	cert, ok, err := a.ingressStore.GetCertificate(r.PathValue("id"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		jsonError(w, http.StatusNotFound, "certificate not found")
		return
	}
	redactManagedCertificate(&cert)
	jsonOK(w, cert)
}

func (a *App) deleteIngressCertificate(w http.ResponseWriter, r *http.Request) {
	if err := a.ingressStore.DeleteCertificate(r.PathValue("id")); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}

func (a *App) issueIngressCertificate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cert, ok, err := a.ingressStore.GetCertificate(id)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		jsonError(w, http.StatusNotFound, "certificate not found")
		return
	}
	cert.Status = ingress.CertPending
	cert.LastError = ""
	cert, err = a.ingressStore.UpsertCertificate(cert)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	go func() {
		_, _ = a.ingressCertService.Issue(context.Background(), id)
	}()
	redactManagedCertificate(&cert)
	jsonOK(w, cert)
}

func (a *App) renewIngressCertificate(w http.ResponseWriter, r *http.Request) {
	cert, err := a.ingressCertService.Renew(r.Context(), r.PathValue("id"))
	if err != nil {
		jsonError(w, ingressErrorStatus(err), err.Error())
		return
	}
	redactManagedCertificate(&cert)
	jsonOK(w, cert)
}

func (a *App) deployIngressCertificate(w http.ResponseWriter, r *http.Request) {
	var req certificateDeployRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cert, err := a.ingressCertService.Deploy(r.Context(), r.PathValue("id"), certificateDeploymentRequests(req))
	if err != nil {
		jsonError(w, ingressErrorStatus(err), err.Error())
		return
	}
	redactManagedCertificate(&cert)
	jsonOK(w, cert)
}

func (a *App) matchIngressCertificate(w http.ResponseWriter, r *http.Request) {
	cert, ok, err := a.ingressCertService.Match(r.URL.Query().Get("domain"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		jsonError(w, http.StatusNotFound, "certificate not found")
		return
	}
	redactManagedCertificate(cert)
	jsonOK(w, cert)
}

func (a *App) getIngressACMEAccount(w http.ResponseWriter, r *http.Request) {
	account, err := a.ingressStore.GetACMEAccount()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, account)
}

func (a *App) saveIngressACMEAccount(w http.ResponseWriter, r *http.Request) {
	var account ingress.ACMEAccount
	if err := json.NewDecoder(r.Body).Decode(&account); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	account.Email = strings.TrimSpace(account.Email)
	account.DirectoryURL = strings.TrimSpace(account.DirectoryURL)
	if err := a.ingressStore.SaveACMEAccount(account); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	saved, err := a.ingressStore.GetACMEAccount()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, saved)
}

func normalizeStringList(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func certificateDeploymentRequests(req certificateDeployRequest) []ingress.CertificateDeploymentRequest {
	if len(req.Deployments) > 0 {
		out := make([]ingress.CertificateDeploymentRequest, 0, len(req.Deployments))
		for _, deployment := range req.Deployments {
			out = append(out, ingress.CertificateDeploymentRequest{
				HostID:            strings.TrimSpace(deployment.HostID),
				CertPath:          strings.TrimSpace(deployment.CertPath),
				KeyPath:           strings.TrimSpace(deployment.KeyPath),
				PostDeployCommand: strings.TrimSpace(deployment.PostDeployCommand),
				SourceType:        "manual",
			})
		}
		return out
	}
	hostIDs := normalizeStringList(req.HostIDs)
	out := make([]ingress.CertificateDeploymentRequest, 0, len(hostIDs))
	for _, hostID := range hostIDs {
		out = append(out, ingress.CertificateDeploymentRequest{
			HostID:            hostID,
			CertPath:          strings.TrimSpace(req.CertPath),
			KeyPath:           strings.TrimSpace(req.KeyPath),
			PostDeployCommand: strings.TrimSpace(req.PostDeployCommand),
			SourceType:        "manual",
		})
	}
	return out
}

func redactManagedCertificate(cert *ingress.ManagedCertificate) {
	if cert == nil || cert.Material == nil {
		return
	}
	copied := *cert.Material
	copied.KeyPEM = ""
	cert.Material = &copied
}
