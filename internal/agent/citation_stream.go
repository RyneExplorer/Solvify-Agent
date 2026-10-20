package agent

import (
	"context"
	"html"
	"regexp"
	"strings"

	"solvify-agent/pkg/eventch"
)

var citationAttribute = regexp.MustCompile(`(?i)([a-z_]+)\s*=\s*(?:"([^"]*)"|'([^']*)')`)

// 仅缓存可能属于引用的未完成片段，普通正文即时输出；代码示例原样保留。
type citationStream struct {
	registry *citationRegistry
	pending  string
	code     string
	fenced   bool
	lineTail string
}

func (s *citationStream) push(text string, final bool) string {
	s.pending += text
	var out strings.Builder
	defer func() {
		line := s.lineTail + out.String()
		if i := strings.LastIndexByte(line, '\n'); i >= 0 {
			line = line[i+1:]
		}
		if len(line) > 4 {
			line = line[len(line)-4:]
		}
		s.lineTail = line
	}()
	for s.pending != "" {
		if s.code != "" {
			if s.fenced {
				end := strings.IndexByte(s.pending, '\n')
				if end < 0 && !final {
					return out.String()
				}
				if end < 0 {
					end = len(s.pending)
				} else {
					end++
				}
				line := s.pending[:end]
				trimmed := strings.TrimLeft(line, " ")
				if citationFenceStart(s.lineTail, out.String()) && len(line)-len(trimmed) <= 3 && strings.HasPrefix(trimmed, s.code) {
					n := 0
					for n < len(trimmed) && trimmed[n] == s.code[0] {
						n++
					}
					if strings.TrimSpace(trimmed[n:]) == "" {
						s.code = ""
						s.fenced = false
					}
				}
				out.WriteString(line)
				s.pending = s.pending[end:]
				continue
			}
			if end := citationCodeSpanEnd(s.pending, len(s.code), final); end >= 0 {
				out.WriteString(s.code)
				out.WriteString(s.pending[:end])
				s.pending = s.pending[end:]
				s.code = ""
				continue
			}
			if !final {
				return out.String()
			}
			// 未闭合的行内反引号是普通正文，其中的引用仍须校验。
			out.WriteString(s.code)
			s.code = ""
			continue
		}
		switch s.pending[0] {
		case '\\':
			if len(s.pending) == 1 && !final {
				return out.String()
			}
			n := min(2, len(s.pending))
			out.WriteString(s.pending[:n])
			s.pending = s.pending[n:]
		case '`', '~':
			n := 0
			for n < len(s.pending) && s.pending[n] == s.pending[0] {
				n++
			}
			if n == len(s.pending) && !final {
				return out.String()
			}
			fenced := n >= 3 && citationFenceStart(s.lineTail, out.String())
			if fenced {
				end := strings.IndexByte(s.pending, '\n')
				if end < 0 && !final {
					return out.String()
				}
				if end < 0 {
					end = len(s.pending)
				}
				if s.pending[0] == '`' && strings.Contains(s.pending[n:end], "`") {
					fenced = false
				}
			}
			if s.pending[0] == '~' && !fenced {
				out.WriteString(s.pending[:n])
				s.pending = s.pending[n:]
				continue
			}
			s.code = s.pending[:n]
			s.fenced = fenced
			if fenced {
				out.WriteString(s.code)
			}
			s.pending = s.pending[n:]
		case '<':
			kind, partial := citationPrefix(s.pending)
			if partial && !final {
				return out.String()
			}
			if partial && len(s.pending) < len(kind)+1 {
				out.WriteString(s.pending)
				s.pending = ""
				continue
			}
			if kind == "" {
				out.WriteByte('<')
				s.pending = s.pending[1:]
				continue
			}
			end := citationTagEnd(s.pending)
			if end < 0 && !final && len(s.pending) < 4096 {
				return out.String()
			}
			if end < 0 {
				out.WriteString("（来源未核验）")
				s.pending = ""
				continue
			}
			tag := s.pending[:end+1]
			s.pending = s.pending[end+1:]
			attrs := map[string]string{}
			for _, match := range citationAttribute.FindAllStringSubmatch(tag, -1) {
				value := match[2]
				if match[3] != "" {
					value = match[3]
				}
				attrs[strings.ToLower(match[1])] = html.UnescapeString(value)
			}
			var source citationSource
			var ok bool
			switch kind {
			case "cite":
				source, ok = s.registry.find(attrs["id"], "", "")
				if !ok && attrs["id"] != "" && !strings.HasPrefix(attrs["id"], "src_") {
					source, ok = s.registry.find("", attrs["id"], "")
				}
			case "kb":
				source, ok = s.registry.find("", attrs["chunk_id"], "")
			case "web":
				source, ok = s.registry.find("", "", attrs["url"])
			}
			if ok {
				out.WriteString(citationTag(source))
			} else {
				out.WriteString("（来源未核验）")
			}
		case '[':
			endLabel := citationClosing(s.pending, '[', ']')
			if endLabel < 0 {
				if !final && len(s.pending) < 4096 {
					return out.String()
				}
				out.WriteByte('[')
				s.pending = s.pending[1:]
				continue
			}
			if endLabel+1 == len(s.pending) && !final {
				return out.String()
			}
			if endLabel+1 >= len(s.pending) || s.pending[endLabel+1] != '(' {
				out.WriteByte('[')
				s.pending = s.pending[1:]
				continue
			}
			endLink := citationClosing(s.pending[endLabel+1:], '(', ')')
			if endLink < 0 && !final && len(s.pending) < 4096 {
				return out.String()
			}
			if endLink < 0 {
				out.WriteString("（链接未核验）")
				s.pending = ""
				continue
			}
			end := endLabel + 1 + endLink
			link := citationDestination(s.pending[endLabel+2 : end])
			if strings.HasPrefix(link, "#") {
				out.WriteByte('[')
				out.WriteString(normalizeCitations(s.registry, s.pending[1:endLabel]))
				out.WriteString(s.pending[endLabel : end+1])
			} else if source, ok := s.registry.find("", "", html.UnescapeString(link)); ok {
				out.WriteString(citationTag(source))
			} else {
				out.WriteString(normalizeCitations(s.registry, s.pending[1:endLabel]))
				out.WriteString("（链接未核验）")
			}
			s.pending = s.pending[end+1:]
		default:
			i := strings.IndexAny(s.pending, "`~<[\\")
			if i < 0 {
				i = len(s.pending)
			}
			out.WriteString(s.pending[:i])
			s.pending = s.pending[i:]
		}
	}
	return out.String()
}

func citationFenceStart(previous, emitted string) bool {
	line := previous + emitted
	if i := strings.LastIndexByte(line, '\n'); i >= 0 {
		line = line[i+1:]
	}
	return len(line) <= 3 && strings.Trim(line, " ") == ""
}

func citationCodeSpanEnd(text string, width int, final bool) int {
	for i := 0; i < len(text); {
		if text[i] != '`' {
			i++
			continue
		}
		start := i
		for i < len(text) && text[i] == '`' {
			i++
		}
		if i == len(text) && !final {
			return -1
		}
		if i-start == width {
			return i
		}
	}
	return -1
}

func citationPrefix(text string) (kind string, partial bool) {
	text = strings.ToLower(text)
	for _, name := range []string{"cite", "kb", "web"} {
		prefix := "<" + name
		if strings.HasPrefix(prefix, text) {
			return name, true
		}
		if strings.HasPrefix(text, prefix) && strings.ContainsAny(text[len(prefix):len(prefix)+1], " \t\r\n/>") {
			return name, false
		}
	}
	return "", false
}

func citationTagEnd(text string) int {
	var quote byte
	for i := 1; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
		} else if c == '>' {
			return i
		}
	}
	return -1
}

func citationClosing(text string, open, close byte) int {
	level := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' {
			i++
			continue
		}
		if text[i] == open {
			level++
		}
		if text[i] == close {
			level--
			if level == 0 {
				return i
			}
		}
	}
	return -1
}

func citationDestination(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "<") {
		if end := strings.IndexByte(text, '>'); end >= 0 {
			return text[1:end]
		}
	}
	if i := strings.IndexAny(text, " \t\n"); i >= 0 {
		return text[:i]
	}
	return text
}

func normalizeCitations(registry *citationRegistry, text string) string {
	stream := citationStream{registry: registry}
	return stream.push(text, true)
}

// 统一公开正文与done全文，Service持久化的也是校验后的内容。
// 生产者关闭后等待消费退出，取消时继续排空，避免增加悬挂goroutine。
func citationEventChannel(ctx context.Context, registry *citationRegistry, out chan<- Event) (chan Event, <-chan struct{}) {
	in, done := make(chan Event, 100), make(chan struct{})
	go func() {
		defer close(done)
		stream := citationStream{registry: registry}
		var lastAnswer Event
		flush := func() {
			if text := stream.push("", true); text != "" {
				ev := lastAnswer
				ev.Type = EventAnswer
				ev.Content = text
				eventch.Send(ctx, out, ev)
			}
		}
		for ev := range in {
			if ev.Type == EventAnswer {
				lastAnswer = ev
				ev.Content = stream.push(ev.Content, false)
				if ev.Content == "" {
					continue
				}
			} else if ev.Type == EventDone || ev.Type == EventError || ev.Type == EventInterrupt {
				flush()
				if ev.Type == EventDone {
					ev.Content = normalizeCitations(registry, ev.Content)
				}
			}
			eventch.Send(ctx, out, ev)
		}
		flush()
	}()
	return in, done
}
