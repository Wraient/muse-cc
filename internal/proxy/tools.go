package proxy

import "sort"

func sortAnthropicTools(tools []AnthropicTool) {
	sort.SliceStable(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})
}

func sortOpenAITools(tools []OpenAITool) {
	sort.SliceStable(tools, func(i, j int) bool {
		return tools[i].Function.Name < tools[j].Function.Name
	})
}
