package app

import (
	"os"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"log/slog"
)

// singBoxConfigV1 旧格式：json/js 为 []string
type singBoxConfigV1 struct {
	Version string   `yaml:"version"`
	JSON    []string `yaml:"json"`
	JS      []string `yaml:"js"`
}

// configV1 仅解析需要迁移的字段
type configV1 struct {
	SingboxLatest singBoxConfigV1 `yaml:"singbox-latest"`
	SingboxOld    singBoxConfigV1 `yaml:"singbox-old"`
}

// resolveDomainV1 旧格式：sub-process.resolve-domain 为布尔值
type resolveDomainV1 struct {
	SubProcess struct {
		ResolveDomain bool `yaml:"resolve-domain"`
	} `yaml:"sub-process"`
}

// migrateConfig 检测配置文件中的旧格式字段，若存在则原地升级并保存。
func (app *App) migrateConfig() error {
	data, err := os.ReadFile(app.configPath)
	if err != nil {
		// 文件不存在等情况交给 loadConfig 处理
		return nil
	}

	content := string(data)
	needWrite := false

	// 迁移 singbox v1 → v2
	var old configV1
	if err := yaml.Unmarshal(data, &old); err == nil {
		if migrateSingBoxV1(&old.SingboxLatest) {
			content = rewriteSingboxBlock(content, "singbox-latest", old.SingboxLatest)
			needWrite = true
		}
		if migrateSingBoxV1(&old.SingboxOld) {
			content = rewriteSingboxBlock(content, "singbox-old", old.SingboxOld)
			needWrite = true
		}
	}

	// 迁移 resolve-domain: false → 新对象格式
	var rd resolveDomainV1
	if err := yaml.Unmarshal(data, &rd); err == nil {
		// resolve-domain 为布尔 → 旧格式
		// 这里只要字段存在就是旧格式，不用再判断 true/false
		if rd.SubProcess.ResolveDomain || !rd.SubProcess.ResolveDomain {
			content = rewriteResolveDomain(content, rd.SubProcess.ResolveDomain)
			needWrite = true
		}
	}

	// 写回文件
	if needWrite {
		if err := os.WriteFile(app.configPath, []byte(content), 0o644); err != nil {
			return err
		}
		slog.Info("配置已自动迁移为新格式")
	}

	return nil
}

// migrateSingBoxV1 检测是否为旧列表格式。
// 若是，取第一个元素留在原字段（仅用于后续文本替换），返回 true。
func migrateSingBoxV1(v1 *singBoxConfigV1) bool {
	return len(v1.JSON) > 0 || len(v1.JS) > 0
}

// rewriteSingboxBlock 在原始 yaml 文本中找到指定 singbox 块，
// 将其 json/js 列表写法替换为字符串写法，其余内容保持不变。
func rewriteSingboxBlock(content, blockKey string, v1 singBoxConfigV1) string {
	lines := strings.Split(content, "\n")

	// 找到块的起始行
	blockStart := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == blockKey+":" {
			blockStart = i
			break
		}
	}
	if blockStart < 0 {
		return content
	}

	// 确定块的缩进深度（用于判断子键范围）
	blockIndent := len(lines[blockStart]) - len(strings.TrimLeft(lines[blockStart], " \t"))

	// 在块范围内处理 json / js 列表
	for i := blockStart + 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		// 退出块范围
		if indent <= blockIndent {
			break
		}

		trimmed := strings.TrimSpace(line)

		// 找到 json: 或 js: 且值为空（旧列表写法的标志）
		var list []string
		var key string

		if strings.HasPrefix(trimmed, "json:") && len(v1.JSON) > 0 {
			key = "json:"
			list = v1.JSON
		} else if strings.HasPrefix(trimmed, "js:") && len(v1.JS) > 0 {
			key = "js:"
			list = v1.JS
		} else {
			continue
		}

		// 替换为字符串写法
		keyIndent := strings.Repeat(" ", indent)
		lines[i] = keyIndent + key + " " + list[0]

		// 删除旧列表项
		j := i + 1
		for j < len(lines) {
			t := strings.TrimSpace(lines[j])
			if t == "" {
				j++
				continue
			}
			if strings.HasPrefix(t, "- ") {
				j++
				continue
			}
			break
		}

		newLines := append([]string{}, lines[:i+1]...)
		newLines = append(newLines, lines[j:]...)
		return strings.Join(newLines, "\n")
	}

	return content
}

// rewriteResolveDomain resolve-domain: false → 新对象格式迁移（含注释）
func rewriteResolveDomain(content string, oldValue bool) string {
	lines := strings.Split(content, "\n")

	// 找到 sub-process 块
	blockStart := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "sub-process:" {
			blockStart = i
			break
		}
	}
	if blockStart < 0 {
		return content
	}

	blockIndent := len(lines[blockStart]) - len(strings.TrimLeft(lines[blockStart], " \t"))

	// 找 resolve-domain: false
	for i := blockStart + 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}

		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent <= blockIndent {
			break
		}

		if strings.HasPrefix(strings.TrimSpace(line), "resolve-domain:") {

			keyIndent := strings.Repeat(" ", indent)

			newBlock := []string{
				keyIndent + "resolve-domain:",
				keyIndent + "  enable: " + strings.ToLower(strconv.FormatBool(oldValue)) + " # 是否开启 DNS 解析",
				keyIndent + "  provider: ali # DNS 服务商（ali / cloudflare / google）",
				keyIndent + "  concurrency: 10 # 并发数，默认10，在代理软件中不要超过20",
				keyIndent + "  timeout: 8000 # 超时（毫秒），默认8000",
				keyIndent + "  edns: \"\" # EDNS 设置",
				keyIndent + "  type: ipv4 # ipv4 / ipv6",
				keyIndent + "  cache: enable # 缓存策略",
				keyIndent + "  cache-ttl: 3600 # 缓存时长(秒)",
			}

			lines[i] = newBlock[0]

			newLines := append([]string{}, lines[:i+1]...)
			newLines = append(newLines, newBlock[1:]...)
			newLines = append(newLines, lines[i+1:]...)
			return strings.Join(newLines, "\n")
		}
	}

	return content
}
