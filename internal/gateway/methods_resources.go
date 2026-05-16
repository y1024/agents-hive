package gateway

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/security"
	"github.com/chef-guo/agents-hive/internal/store"
)

// resourceSaveRequest 外部资源保存请求
type resourceSaveRequest struct {
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	Environment string  `json:"environment"`
	Description string  `json:"description"`
	Connection  string  `json:"connection"`
	Endpoint    string  `json:"endpoint"`
	Credentials *string `json:"credentials"`
	ReadOnly    bool    `json:"read_only"`
	Enabled     bool    `json:"enabled"`
}

// registerResourceMethods 注册外部资源管理相关 RPC 方法
func registerResourceMethods(gw *Gateway, deps Deps) {
	// resources.list — 列出所有外部资源
	gw.Register(MethodDef{
		Name:        "resources.list",
		Description: "列出所有外部资源配置",
		AuthScope:   "admin",
		Handler: func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
			if deps.Store == nil {
				return nil, errs.New(errs.CodeInternal, "存储后端未初始化")
			}

			records, err := deps.Store.ListExternalResources(ctx)
			if err != nil {
				return nil, errs.Wrap(errs.CodeInternal, "查询外部资源列表失败", err)
			}

			return json.Marshal(map[string]any{
				"resources": redactExternalResources(records),
			})
		},
	})

	// resources.get — 获取单个外部资源
	gw.Register(MethodDef{
		Name:        "resources.get",
		Description: "根据名称获取单个外部资源配置",
		AuthScope:   "admin",
		Handler: func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
			if deps.Store == nil {
				return nil, errs.New(errs.CodeInternal, "存储后端未初始化")
			}

			var p struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, errs.Wrap(errs.CodeInvalidArgument, "解析请求参数失败", err)
			}
			if p.Name == "" {
				return nil, errs.New(errs.CodeInvalidArgument, "缺少 name 参数")
			}

			rec, err := deps.Store.GetExternalResource(ctx, p.Name)
			if err != nil {
				return nil, errs.Wrap(errs.CodeNotFound, "外部资源未找到: "+p.Name, err)
			}

			return json.Marshal(redactExternalResource(rec))
		},
	})

	// resources.save — 创建或更新外部资源（UPSERT）
	gw.Register(MethodDef{
		Name:        "resources.save",
		Description: "创建或更新外部资源配置（UPSERT）",
		AuthScope:   "admin",
		Handler: func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
			if deps.Store == nil {
				return nil, errs.New(errs.CodeInternal, "存储后端未初始化")
			}

			var req resourceSaveRequest
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, errs.Wrap(errs.CodeInvalidArgument, "解析资源保存请求失败", err)
			}
			if req.Name == "" {
				return nil, errs.New(errs.CodeInvalidArgument, "缺少 name 字段")
			}

			credentials := ""
			if req.Credentials != nil {
				credentials = *req.Credentials
			}
			if req.Credentials == nil || security.HasRedactedMarker(credentials) {
				existing, err := deps.Store.GetExternalResource(ctx, req.Name)
				if err != nil {
					if req.Credentials != nil {
						return nil, errs.New(errs.CodeInvalidArgument, "credentials 包含脱敏占位，请输入完整凭证")
					}
				} else if req.Credentials == nil {
					credentials = existing.Credentials
				} else {
					merged, err := mergeExternalResourceCredentials(existing.Credentials, credentials)
					if err != nil {
						return nil, err
					}
					credentials = merged
				}
			}

			if _, err := deps.Store.GetExternalResource(ctx, req.Name); err == nil {
				update := store.ExternalResourceUpdate{
					Type:        &req.Type,
					Environment: &req.Environment,
					Description: &req.Description,
					Connection:  &req.Connection,
					Endpoint:    &req.Endpoint,
					Credentials: &credentials,
					ReadOnly:    &req.ReadOnly,
					Enabled:     &req.Enabled,
				}
				if err := deps.Store.UpdateExternalResource(ctx, req.Name, update); err != nil {
					return nil, errs.Wrap(errs.CodeInternal, "更新外部资源失败", err)
				}
			} else {
				rec := &store.ExternalResourceRecord{
					Name:        req.Name,
					Type:        req.Type,
					Environment: req.Environment,
					Description: req.Description,
					Connection:  req.Connection,
					Endpoint:    req.Endpoint,
					Credentials: credentials,
					ReadOnly:    req.ReadOnly,
					Enabled:     req.Enabled,
				}
				if err := deps.Store.CreateExternalResource(ctx, rec); err != nil {
					return nil, errs.Wrap(errs.CodeInternal, "创建外部资源失败", err)
				}
			}

			return json.Marshal(map[string]string{
				"status": "ok",
				"name":   req.Name,
			})
		},
	})

	// resources.delete — 删除外部资源
	gw.Register(MethodDef{
		Name:        "resources.delete",
		Description: "根据名称删除外部资源配置",
		AuthScope:   "admin",
		Handler: func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
			if deps.Store == nil {
				return nil, errs.New(errs.CodeInternal, "存储后端未初始化")
			}

			var p struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, errs.Wrap(errs.CodeInvalidArgument, "解析请求参数失败", err)
			}
			if p.Name == "" {
				return nil, errs.New(errs.CodeInvalidArgument, "缺少 name 参数")
			}

			if err := deps.Store.DeleteExternalResource(ctx, p.Name); err != nil {
				return nil, errs.Wrap(errs.CodeInternal, "删除外部资源失败: "+p.Name, err)
			}

			return json.Marshal(map[string]string{
				"status": "ok",
				"name":   p.Name,
			})
		},
	})
}

func redactExternalResources(records []*store.ExternalResourceRecord) []*store.ExternalResourceRecord {
	out := make([]*store.ExternalResourceRecord, 0, len(records))
	for _, rec := range records {
		out = append(out, redactExternalResource(rec))
	}
	return out
}

func redactExternalResource(rec *store.ExternalResourceRecord) *store.ExternalResourceRecord {
	if rec == nil {
		return nil
	}
	cp := *rec
	if cp.Credentials != "" {
		cp.Credentials = security.RedactedValue
	}
	return &cp
}

func mergeExternalResourceCredentials(existing, incoming string) (string, error) {
	if !security.HasRedactedMarker(incoming) {
		return incoming, nil
	}
	if strings.TrimSpace(existing) == "" {
		return "", errs.New(errs.CodeInvalidArgument, "credentials 包含脱敏占位，请输入完整凭证")
	}
	if strings.TrimSpace(incoming) == security.RedactedValue {
		return existing, nil
	}

	merged, ok := mergeCredentialJSON(existing, incoming)
	if ok {
		return merged, nil
	}
	return existing, nil
}

func mergeCredentialJSON(existing, incoming string) (string, bool) {
	if !json.Valid([]byte(incoming)) || !json.Valid([]byte(existing)) {
		return "", false
	}
	var incomingValue any
	if err := json.Unmarshal([]byte(incoming), &incomingValue); err != nil {
		return "", false
	}
	var existingValue any
	if err := json.Unmarshal([]byte(existing), &existingValue); err != nil {
		return "", false
	}
	merged := security.PreserveRedactedValues(incomingValue, existingValue)
	out, err := json.Marshal(merged)
	if err != nil {
		return "", false
	}
	return string(out), true
}
