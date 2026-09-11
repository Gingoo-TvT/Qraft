package service

import (
	"context"
	"fmt"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"strings"
)

type setSavedProviderSettings interface {
	EffectiveRuntimeConfig(context.Context, string) (*domain.LLMRuntimeConfig, error)
	SavedRuntimeConfig(context.Context, string) (*domain.LLMRuntimeConfig, bool, error)
}
type setRuntimeKeys interface {
	Put(context.Context, string) (string, error)
}
type ProblemSetProviderResolver func(context.Context) (*domain.ProviderRuntimeConfig, error)

// Resolve per batch so large sets do not retain expired runtime references.
func NewProblemSetProviderResolver(settings setSavedProviderSettings, keys setRuntimeKeys) ProblemSetProviderResolver {
	return func(ctx context.Context) (*domain.ProviderRuntimeConfig, error) {
		g, err := settings.EffectiveRuntimeConfig(ctx, "statement")
		if err != nil {
			return nil, err
		}
		if g == nil || strings.TrimSpace(g.Model) == "" {
			return nil, fmt.Errorf("not configured: 请先配置出题模型")
		}
		v, saved, err := settings.SavedRuntimeConfig(ctx, "verification")
		if err != nil {
			return nil, err
		}
		if !saved {
			v = g
		}
		r, saved, err := settings.SavedRuntimeConfig(ctx, "review")
		if err != nil {
			return nil, err
		}
		if !saved {
			r = v
		}
		prepare := func(in *domain.LLMRuntimeConfig) (*domain.LLMRuntimeConfig, error) {
			if in == nil {
				return nil, fmt.Errorf("not configured: 模型配置不完整")
			}
			out := *in
			if strings.TrimSpace(out.APIKey) != "" {
				if out.APIKeyRef != "" || keys == nil {
					return nil, fmt.Errorf("模型密钥配置冲突")
				}
				ref, err := keys.Put(ctx, out.APIKey)
				if err != nil {
					return nil, err
				}
				out.APIKey = ""
				out.APIKeyRef = ref
			}
			return &out, nil
		}
		g, err = prepare(g)
		if err != nil {
			return nil, err
		}
		v, err = prepare(v)
		if err != nil {
			return nil, err
		}
		r, err = prepare(r)
		if err != nil {
			return nil, err
		}
		out := &domain.ProviderRuntimeConfig{Statement: g, Verification: v, Review: r}
		if err = out.Validate(); err != nil {
			return nil, err
		}
		return out, nil
	}
}
