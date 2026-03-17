package app

import (
	"os"
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

// migrateConfig 检测配置文件中的旧格式字段，若存在则原地升级并保存。
func (app *App) migrateConfig() error {
	data, err := os.ReadFile(app.configPath)
	if err != nil {
		// 文件不存在等情况交给 loadConfig 处理
		return nil
	}

	var old configV1
	// 解析失败不阻断流程（可能是全新配置）
	if err := yaml.Unmarshal(data, &old); err != nil {
		return nil
	}

	latestMigrated := migrateSingBoxV1(&old.SingboxLatest)
	oldMigrated := migrateSingBoxV1(&old.SingboxOld)

	if !latestMigrated && !oldMigrated {
		// 无需迁移
		return nil
	}

	// 将迁移结果写回：用文本替换，保留文件其余内容和注释
	content := string(data)
	if latestMigrated {
		content = rewriteSingboxBlock(content, "singbox-latest", old.SingboxLatest)
	}
	if oldMigrated {
		content = rewriteSingboxBlock(content, "singbox-old", old.SingboxOld)
	}

	if err := os.WriteFile(app.configPath, []byte(content), 0o644); err != nil {
		return err
	}

	slog.Info("singbox 配置已自动迁移为新格式")
	return nil
}

// migrateSingBoxV1 检测是否为旧列表格式。
// 若是，取第一个元素留在原字段（仅用于后续文本替换），返回 true。
func migrateSingBoxV1(v1 *singBoxConfigV1) bool {
	if len(v1.JSON) == 0 && len(v1.JS) == 0 {
		return false
	}
	// 列表长度 >0 说明是旧格式，取第一个元素即可（其余忽略）
	return true
}

// rewriteSingboxBlock 在原始 yaml 文本中找到指定 singbox 块，
// 将其 json/js 列表写法替换为字符串写法，其余内容保持不变。
//
// 替换规则（只处理直属于该块的 json/js 键）：
//
//	json:          →  json: https://...
//	  - https://...
func rewriteSingboxBlock(content, blockKey string, v1 singBoxConfigV1) string {
	lines := strings.Split(content, "\n")

	// 找到块的起始行
	blockStart := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == blockKey+":" || strings.HasPrefix(trimmed, blockKey+":") {
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
	i := blockStart + 1
	for i < len(lines) {
		line := lines[i]
		if line == "" {
			i++
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		// 退出块范围
		if indent <= blockIndent && strings.TrimSpace(line) != "" {
			break
		}

		trimmed := strings.TrimSpace(line)

		// 找到 json: 或 js: 且值为空（旧列表写法的标志）
		var fieldValue string
		var matched bool
		if strings.HasPrefix(trimmed, "json:") {
			fieldValue = strings.TrimSpace(strings.TrimPrefix(trimmed, "json:"))
			matched = fieldValue == "" && len(v1.JSON) > 0
		} else if strings.HasPrefix(trimmed, "js:") {
			fieldValue = strings.TrimSpace(strings.TrimPrefix(trimmed, "js:"))
			matched = fieldValue == "" && len(v1.JS) > 0
		}

		if matched {
			// 收集下面的列表项
			var listItems []string
			j := i + 1
			for j < len(lines) {
				itemLine := lines[j]
				itemTrimmed := strings.TrimSpace(itemLine)
				if strings.HasPrefix(itemTrimmed, "- ") {
					listItems = append(listItems, strings.TrimPrefix(itemTrimmed, "- "))
					j++
				} else {
					break
				}
			}

			if len(listItems) > 0 {
				// 用第一个值替换当前行，删除列表项行
				keyIndent := strings.Repeat(" ", indent)
				var newValue string
				if strings.HasPrefix(trimmed, "json:") {
					newValue = keyIndent + "json: " + listItems[0]
				} else {
					newValue = keyIndent + "js: " + listItems[0]
				}
				// 替换当前行，删除列表项
				newLines := make([]string, 0, len(lines)-(j-i-1))
				newLines = append(newLines, lines[:i]...)
				newLines = append(newLines, newValue)
				newLines = append(newLines, lines[j:]...)
				lines = newLines
				// i 不变，继续处理同位置（现在已是下一个键）
				continue
			}
		}
		i++
	}

	return strings.Join(lines, "\n")
}
