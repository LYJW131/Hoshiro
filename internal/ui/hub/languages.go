package hub

// languages is format.ts's LANGUAGES.
var languages = []struct{ code, label string }{
	{"zh-CN", "简体中文（中国大陆）"},
	{"zh-TW", "繁體中文（台湾）"},
	{"zh-HK", "繁體中文（香港）"},
	{"en-US", "英语（美国）"},
	{"en-GB", "英语（英国）"},
	{"en-AU", "英语（澳大利亚）"},
	{"en-CA", "英语（加拿大）"},
	{"ja-JP", "日语"},
	{"ko-KR", "韩语"},
	{"fr-FR", "法语（法国）"},
	{"fr-CA", "法语（加拿大）"},
	{"de-DE", "德语"},
	{"es-ES", "西班牙语（西班牙）"},
	{"es-MX", "西班牙语（墨西哥）"},
	{"es-US", "西班牙语（美国）"},
	{"pt-BR", "葡萄牙语（巴西）"},
	{"pt-PT", "葡萄牙语（葡萄牙）"},
	{"it-IT", "意大利语"},
	{"ru-RU", "俄语"},
	{"nl-NL", "荷兰语"},
	{"pl-PL", "波兰语"},
	{"tr-TR", "土耳其语"},
	{"uk-UA", "乌克兰语"},
	{"vi-VN", "越南语"},
	{"th-TH", "泰语"},
	{"id-ID", "印尼语"},
	{"ms-MY", "马来语"},
	{"hi-IN", "印地语"},
	{"ar-SA", "阿拉伯语（沙特阿拉伯）"},
	{"ar-EG", "阿拉伯语（埃及）"},
	{"he-IL", "希伯来语"},
	{"fa-IR", "波斯语"},
	{"sv-SE", "瑞典语"},
	{"da-DK", "丹麦语"},
	{"nb-NO", "挪威语"},
	{"fi-FI", "芬兰语"},
	{"cs-CZ", "捷克语"},
	{"el-GR", "希腊语"},
	{"hu-HU", "匈牙利语"},
	{"ro-RO", "罗马尼亚语"},
}

func languageLabel(code string) string {
	for _, l := range languages {
		if l.code == code {
			return l.label
		}
	}
	return code
}
