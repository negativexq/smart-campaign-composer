package content

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type streamState int

const (
	stateBeforeKey streamState = iota
	stateInKey
	stateAfterKey
	stateBeforeValue
	stateInValue
	stateSkippingValue
)

// FieldStream turns partial model JSON into text per message field, so a preview
// can grow before the whole output exists. Its output is display-only: nothing
// is trusted until ParseGenerated accepts the complete result.
//
// It reads a flat object and survives any chunk boundary. On malformed input it
// shows less text rather than failing, since validation decides the outcome.
type FieldStream struct {
	emit    func(field, text string)
	state   streamState
	depth   int
	key     strings.Builder
	field   string
	escaped bool
	unicode []byte
	high    rune
	pending []byte
	inSkip  bool
}

// NewFieldStream emits decoded text for the known message fields.
func NewFieldStream(emit func(field, text string)) *FieldStream {
	return &FieldStream{emit: emit}
}

var streamedFields = []string{FieldSMS, FieldEmailSubject, FieldEmailBody}

// Write consumes the next chunk of output.
func (s *FieldStream) Write(chunk string) {
	var out strings.Builder
	data := append(s.pending, chunk...)
	s.pending = nil
	for index := 0; index < len(data); index++ {
		char := data[index]
		switch s.state {
		case stateBeforeKey:
			s.depth = trackDepth(s.depth, char)
			if char == '"' && s.depth == 1 {
				s.state = stateInKey
				s.key.Reset()
			}
		case stateInKey:
			if char == '"' {
				s.state = stateAfterKey
			} else {
				s.key.WriteByte(char)
			}
		case stateAfterKey:
			if char == ':' {
				s.state = stateBeforeValue
			}
		case stateBeforeValue:
			switch {
			case isSpace(char):
			case char == '"':
				s.state = stateInValue
				s.field = ""
				if slices.Contains(streamedFields, s.key.String()) {
					s.field = s.key.String()
				}
			default:
				s.state = stateSkippingValue
				s.depth = trackDepth(s.depth, char)
			}
		case stateInValue:
			// Hold back a multi-byte character split across chunks.
			if !s.escaped && len(s.unicode) == 0 && char >= utf8.RuneSelf && !utf8.FullRune(data[index:]) {
				s.pending = append(s.pending, data[index:]...)
				index = len(data)
				continue
			}
			s.readValueByte(char, &out)
		case stateSkippingValue:
			s.skipValueByte(char)
		}
	}
	s.flush(&out)
}

func (s *FieldStream) readValueByte(char byte, out *strings.Builder) {
	switch {
	case len(s.unicode) > 0 || s.escaped && char == 'u':
		s.readUnicodeEscape(char, out)
	case s.escaped:
		s.escaped = false
		if decoded, ok := simpleEscapes[char]; ok {
			out.WriteByte(decoded)
		}
	case char == '\\':
		s.escaped = true
	case char == '"':
		s.flush(out)
		s.state = stateBeforeKey
	default:
		out.WriteByte(char)
	}
}

var simpleEscapes = map[byte]byte{
	'"': '"', '\\': '\\', '/': '/', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t',
}

func (s *FieldStream) readUnicodeEscape(char byte, out *strings.Builder) {
	if s.escaped {
		s.escaped = false
		s.unicode = []byte{'u'}
		return
	}
	s.unicode = append(s.unicode, char)
	if len(s.unicode) < 5 {
		return
	}
	value, err := strconv.ParseUint(string(s.unicode[1:]), 16, 32)
	s.unicode = nil
	if err != nil {
		return
	}
	code := rune(value)
	switch {
	case utf16.IsSurrogate(code) && s.high == 0:
		s.high = code
	case utf16.IsSurrogate(code):
		out.WriteRune(utf16.DecodeRune(s.high, code))
		s.high = 0
	default:
		s.high = 0
		out.WriteRune(code)
	}
}

func (s *FieldStream) skipValueByte(char byte) {
	if s.inSkip {
		switch {
		case s.escaped:
			s.escaped = false
		case char == '\\':
			s.escaped = true
		case char == '"':
			s.inSkip = false
		}
		return
	}
	switch {
	case char == '"':
		s.inSkip = true
	case char == ',' && s.depth == 1:
		s.state = stateBeforeKey
	default:
		s.depth = trackDepth(s.depth, char)
	}
}

func (s *FieldStream) flush(out *strings.Builder) {
	if out.Len() > 0 && s.field != "" {
		s.emit(s.field, out.String())
	}
	out.Reset()
}

func trackDepth(depth int, char byte) int {
	switch char {
	case '{', '[':
		return depth + 1
	case '}', ']':
		return depth - 1
	}
	return depth
}

func isSpace(char byte) bool {
	return char == ' ' || char == '\n' || char == '\r' || char == '\t'
}
