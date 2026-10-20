package app

import (
	"fmt"
	"log"
	"strings"
)

// sourceSummary 单个模型来源在启动横幅里的概览。
type sourceSummary struct {
	Key      string
	Label    string
	Enabled  bool
	Accounts int
}

// sourceSummaries 汇总各模型来源的启用状态与账号数。
// 本项目是多来源聚合网关, 启动信息不应该只反映 cline 账号池, 所以这里按
// 来源逐个统计(口径与后台「各模型来源模型数」卡片一致)。
// 注意: 只统计账号与启用状态, 模型数在目录同步完成后由 logModelSourceSummary 输出。
func sourceSummaries() []sourceSummary {
	out := []sourceSummary{}

	out = append(out, sourceSummary{
		Key: "cline-free", Label: "Cline", Enabled: true,
		Accounts: len(loadPool().Accounts),
	})

	if cfg := getZenConfig(); cfg != nil {
		out = append(out, sourceSummary{Key: "opencode-zen", Label: "OpenCode Zen", Enabled: cfg.Enabled})
	}

	for _, v := range []*wbVariant{wbVariantCN, wbVariantINTL} {
		out = append(out, sourceSummary{
			Key: v.Provider, Label: v.DisplayName,
			Enabled:  wbVariantEnabled(v),
			Accounts: len(wbActiveAccounts(v.Provider)),
		})
	}

	if cfg := getQoderConfig(); cfg != nil {
		out = append(out, sourceSummary{
			Key: "qoder", Label: "Qoder", Enabled: cfg.Enabled,
			Accounts: len(qoderLoadAccounts()),
		})
	}

	if cfg := getTraeConfig(); cfg != nil {
		out = append(out, sourceSummary{
			Key: "trae", Label: "Trae", Enabled: cfg.Enabled,
			Accounts: len(traeLoadAccounts()),
		})
	}

	return out
}

// sourceAccountsLine 各来源账号数的紧凑描述, 如 "Cline 3, WorkBuddy 1"。
func sourceAccountsLine() string {
	parts := []string{}
	for _, s := range sourceSummaries() {
		if s.Accounts > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", s.Label, s.Accounts))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// totalSourceAccounts 所有来源的账号总数(跨来源)。
func totalSourceAccounts() int {
	n := 0
	for _, s := range sourceSummaries() {
		n += s.Accounts
	}
	return n
}

// startupBannerLines 启动横幅里的来源概览行(多来源视角)。
func startupBannerLines() []string {
	sums := sourceSummaries()
	labels := make([]string, 0, len(sums))
	for _, s := range sums {
		labels = append(labels, s.Label)
	}
	lines := []string{"  Sources:  " + strings.Join(labels, ", ")}

	total := totalSourceAccounts()
	if total > 0 {
		lines = append(lines, fmt.Sprintf("  Accounts: %d total (%s)", total, sourceAccountsLine()))
	} else {
		lines = append(lines, "  Accounts: none (可在后台管理页添加)")
	}
	lines = append(lines, fmt.Sprintf("  Default:  %s", getDefaultModel()))
	return lines
}

// logModelSourceSummary 目录同步完成后输出一次各来源模型数概览。
func logModelSourceSummary() {
	stats := modelStatsSnapshot()
	parts := make([]string, 0, len(stats))
	for _, s := range stats {
		parts = append(parts, fmt.Sprintf("%s %d/%d free", s.Label, s.Free, s.Total))
	}
	log.Printf("  models:   %s", strings.Join(parts, " | "))
}
