// targets.go 从项目配置中提取本机前端浏览器调试目标。
//
// 职责：
//   - 筛选启用 AI 调试的 local web deployment
//   - 合成并校验只能打开 loopback 的目标 URL
//
// 边界：
//   - 不启动浏览器或服务进程
//   - 不支持远端 deployment 或 tunnel
package browserdebug

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/xsxdot/super-dev/agent/model"
)

// ListTargets 返回项目中所有 v1 支持的本机前端调试目标。
func ListTargets(projects []model.Project) []Target {
	targets := []Target{}
	for _, project := range projects {
		for _, service := range project.Services {
			for _, dep := range service.Deployments {
				if !isLocalDeployment(dep) || dep.Web == nil || !dep.Web.Enabled || !dep.Web.AIDebug.Enabled {
					continue
				}
				if _, err := BuildTargetURL(dep.Web.URL, dep.Web.DefaultPath); err != nil {
					continue
				}
				targets = append(targets, Target{
					ProjectID:    project.ID,
					ProjectName:  project.Name,
					ServiceID:    service.ID,
					ServiceName:  service.Name,
					DeploymentID: dep.ID,
					EnvName:      dep.EnvName,
					BaseURL:      strings.TrimRight(dep.Web.URL, "/"),
					DefaultPath:  defaultPath(dep.Web.DefaultPath),
				})
			}
		}
	}
	return targets
}

func isLocalDeployment(dep model.Deployment) bool {
	return dep.Location == "" || dep.Location == model.LocationLocal
}

// BuildTargetURL 合成最终打开 URL，并强制限制为 loopback HTTP(S) 地址。
func BuildTargetURL(baseURL string, path string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("web.url must be a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("web.url must use http or https")
	}
	host := strings.Trim(u.Hostname(), "[]")
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return "", fmt.Errorf("web.url must be loopback")
	}
	u.Path = defaultPath(path)
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func defaultPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}
