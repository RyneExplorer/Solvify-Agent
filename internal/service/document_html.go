package service

import (
	"strings"

	"golang.org/x/net/html"
)

// HTML按节点提取可读文本；正文语义符号由解析器解码，不作为分词分隔符删除。
func htmlDocumentText(content string) string {
	z := html.NewTokenizer(strings.NewReader(content))
	var out strings.Builder
	skip, depth := "", 0
	boundary := func() {
		if out.Len() > 0 {
			out.WriteByte('\n')
		}
	}
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			return out.String() // 输入是内存字符串，结束表示EOF；容忍不完整HTML。
		}
		token := z.Token()
		if skip != "" {
			if token.Data == skip {
				if kind == html.StartTagToken {
					depth++
				} else if kind == html.EndTagToken {
					depth--
					if depth == 0 {
						skip = ""
					}
				}
			}
			continue
		}
		if kind == html.TextToken {
			out.WriteString(token.Data)
			continue
		}
		if kind != html.StartTagToken && kind != html.EndTagToken && kind != html.SelfClosingTagToken {
			continue
		}
		if kind == html.StartTagToken {
			switch token.Data {
			case "head", "script", "style", "template", "noscript":
				skip, depth = token.Data, 1
				continue
			}
		}
		switch token.Data {
		case "p", "div", "section", "article", "header", "footer", "main", "h1", "h2", "h3", "h4", "h5", "h6", "li", "ul", "ol", "pre", "blockquote", "table", "tr", "br", "hr":
			boundary()
		case "td", "th":
			if kind == html.EndTagToken {
				out.WriteByte('\t')
			}
		}
	}
}
