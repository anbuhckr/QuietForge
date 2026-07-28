package util

import (
	"encoding/json"
	"fmt"
	"quietforge/provider"
	"regexp"
	"strings"
	"time"
)

var (
	// Regex matching <invoke name="...">...</invoke> or <eriinvoke name="...">...</eriinvoke> or <auvoke name="...">...</auvoke>
	invokeBlockRegex = regexp.MustCompile(`(?is)<(?:[a-zA-Z0-9_-]+)?invoke\s+name=["']([^"']+)["']\s*>(.*?)</(?:[a-zA-Z0-9_-]+)?invoke>`)

	// Regex matching parameter attributes e.g. <parameter name="foo">bar</parameter> or <uriParameter name="foo">bar</uriParameter>
	paramAttrRegex = regexp.MustCompile(`(?is)<(?:[a-zA-Z0-9_-]+)?parameter\s+name=["']([^"']+)["'][^>]*>(.*?)</(?:[a-zA-Z0-9_-]+)?parameter>`)

	// Regex matching generic tag pairs e.g. <command>value</command>
	genericTagRegex = regexp.MustCompile(`(?is)<([a-zA-Z0-9_]+)(?:\s+[^>]+)?>(.*?)</([a-zA-Z0-9_]+)>`)

	// Regex matching <tool_call>\s*<name>([^<]+)</name>\s*<arguments>(.*?)</arguments>\s*</tool_call>
	toolCallTagRegex = regexp.MustCompile(`(?is)<tool_call>\s*<name>([^<]+)</name>\s*<arguments>(.*?)</arguments>\s*</tool_call>`)
)

func ParseXMLToolCalls(content string) []provider.ToolCall {
	var toolCalls []provider.ToolCall
	if strings.TrimSpace(content) == "" {
		return toolCalls
	}

	// 1. Try matching <invoke name="...">...</invoke> blocks
	matches := invokeBlockRegex.FindAllStringSubmatch(content, -1)
	for i, match := range matches {
		if len(match) < 3 {
			continue
		}
		toolName := strings.TrimSpace(match[1])
		body := match[2]

		argsMap := make(map[string]any)

		// Check for parameter attributes e.g. <parameter name="foo">bar</parameter> or <uriParameter name="foo">bar</uriParameter>
		attrMatches := paramAttrRegex.FindAllStringSubmatch(body, -1)
		if len(attrMatches) > 0 {
			for _, am := range attrMatches {
				if len(am) >= 3 {
					key := strings.TrimSpace(am[1])
					val := strings.TrimSpace(am[2])
					argsMap[key] = coerceValue(val)
				}
			}
		} else {
			// Check for generic tag parameters e.g. <command>Select-String...</command>
			tagMatches := genericTagRegex.FindAllStringSubmatch(body, -1)
			for _, tm := range tagMatches {
				if len(tm) >= 4 {
					startTag := strings.TrimSpace(tm[1])
					val := strings.TrimSpace(tm[2])
					endTag := strings.TrimSpace(tm[3])
					if startTag == endTag && startTag != "parameter" && startTag != "uriParameter" {
						argsMap[startTag] = coerceValue(val)
					}
				}
			}
		}

		argsBytes, err := json.Marshal(argsMap)
		if err != nil {
			argsBytes = []byte("{}")
		}

		callID := fmt.Sprintf("call_fallback_xml_%d_%d", time.Now().UnixNano(), i)
		toolCalls = append(toolCalls, provider.ToolCall{
			ID:        callID,
			Name:      toolName,
			Arguments: string(argsBytes),
		})
	}

	// 2. If no invoke blocks found, try matching <tool_call><name>...</name><arguments>...</arguments></tool_call>
	if len(toolCalls) == 0 {
		tcMatches := toolCallTagRegex.FindAllStringSubmatch(content, -1)
		for i, match := range tcMatches {
			if len(match) < 3 {
				continue
			}
			toolName := strings.TrimSpace(match[1])
			argsRaw := strings.TrimSpace(match[2])

			var parsedArgs map[string]any
			if err := json.Unmarshal([]byte(argsRaw), &parsedArgs); err != nil {
				parsedArgs = map[string]any{"input": argsRaw}
			}
			argsBytes, _ := json.Marshal(parsedArgs)

			callID := fmt.Sprintf("call_fallback_xml_%d_%d", time.Now().UnixNano(), i)
			toolCalls = append(toolCalls, provider.ToolCall{
				ID:        callID,
				Name:      toolName,
				Arguments: string(argsBytes),
			})
		}
	}

	return toolCalls
}

func coerceValue(val string) any {
	if val == "true" {
		return true
	}
	if val == "false" {
		return false
	}
	var intVal int
	if _, err := fmt.Sscanf(val, "%d", &intVal); err == nil && fmt.Sprintf("%d", intVal) == val {
		return intVal
	}
	return val
}
