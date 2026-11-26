package jmespath

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

type token struct {
	tokenType tokType
	value     string
	position  int
	length    int
}

type tokType int

const eof = -1

type Lexer struct {
	expression string
	currentPos int
	lastWidth  int
	buf        bytes.Buffer
}

type SyntaxError struct {
	msg        string
	Expression string
	Offset     int
}

func (e SyntaxError) Error() string {
	return "SyntaxError: " + e.msg
}

func (e SyntaxError) HighlightLocation() string {
	return e.Expression + "\n" + strings.Repeat(" ", e.Offset) + "^"
}

//go:generate stringer -type=tokType
const (
	tUnknown tokType = iota
	tStar
	tDot
	tFilter
	tFlatten
	tLparen
	tRparen
	tLbracket
	tRbracket
	tLbrace
	tRbrace
	tOr
	tPipe
	tNumber
	tUnquotedIdentifier
	tQuotedIdentifier
	tComma
	tColon
	tLT
	tLTE
	tGT
	tGTE
	tEQ
	tNE
	tJSONLiteral
	tStringLiteral
	tCurrent
	tExpref
	tAnd
	tNot
	tEOF
)

var basicTokens = map[rune]tokType{
	'.': tDot,
	'*': tStar,
	',': tComma,
	':': tColon,
	'{': tLbrace,
	'}': tRbrace,
	']': tRbracket,
	'(': tLparen,
	')': tRparen,
	'@': tCurrent,
}

const identifierStartBits uint64 = 576460745995190270

var identifierTrailingBits = [2]uint64{287948901175001088, 576460745995190270}

var whiteSpace = map[rune]bool{
	' ': true, '\t': true, '\n': true, '\r': true,
}

func (t token) String() string {
	return fmt.Sprintf("Token{%+v, %s, %d, %d}",
		t.tokenType, t.value, t.position, t.length)
}

func NewLexer() *Lexer {
	lexer := Lexer{}
	return &lexer
}

func (lexer *Lexer) next() rune {
	if lexer.currentPos >= len(lexer.expression) {
		lexer.lastWidth = 0
		return eof
	}
	r, w := utf8.DecodeRuneInString(lexer.expression[lexer.currentPos:])
	lexer.lastWidth = w
	lexer.currentPos += w
	return r
}

func (lexer *Lexer) back() {
	lexer.currentPos -= lexer.lastWidth
}

func (lexer *Lexer) peek() rune {
	t := lexer.next()
	lexer.back()
	return t
}

func (lexer *Lexer) tokenize(expression string) ([]token, error) {
	var tokens []token
	lexer.expression = expression
	lexer.currentPos = 0
	lexer.lastWidth = 0
loop:
	for {
		r := lexer.next()
		if identifierStartBits&(1<<(uint64(r)-64)) > 0 {
			t := lexer.consumeUnquotedIdentifier()
			tokens = append(tokens, t)
		} else if val, ok := basicTokens[r]; ok {
			t := token{
				tokenType: val,
				value:     string(r),
				position:  lexer.currentPos - lexer.lastWidth,
				length:    1,
			}
			tokens = append(tokens, t)
		} else if r == '-' || (r >= '0' && r <= '9') {
			t := lexer.consumeNumber()
			tokens = append(tokens, t)
		} else if r == '[' {
			t := lexer.consumeLBracket()
			tokens = append(tokens, t)
		} else if r == '"' {
			t, err := lexer.consumeQuotedIdentifier()
			if err != nil {
				return tokens, err
			}
			tokens = append(tokens, t)
		} else if r == '\'' {
			t, err := lexer.consumeRawStringLiteral()
			if err != nil {
				return tokens, err
			}
			tokens = append(tokens, t)
		} else if r == '`' {
			t, err := lexer.consumeLiteral()
			if err != nil {
				return tokens, err
			}
			tokens = append(tokens, t)
		} else if r == '|' {
			t := lexer.matchOrElse(r, '|', tOr, tPipe)
			tokens = append(tokens, t)
		} else if r == '<' {
			t := lexer.matchOrElse(r, '=', tLTE, tLT)
			tokens = append(tokens, t)
		} else if r == '>' {
			t := lexer.matchOrElse(r, '=', tGTE, tGT)
			tokens = append(tokens, t)
		} else if r == '!' {
			t := lexer.matchOrElse(r, '=', tNE, tNot)
			tokens = append(tokens, t)
		} else if r == '=' {
			t := lexer.matchOrElse(r, '=', tEQ, tUnknown)
			tokens = append(tokens, t)
		} else if r == '&' {
			t := lexer.matchOrElse(r, '&', tAnd, tExpref)
			tokens = append(tokens, t)
		} else if r == eof {
			break loop
		} else if _, ok := whiteSpace[r]; ok {
		} else {
			return tokens, lexer.syntaxError(fmt.Sprintf("Unknown char: %s", strconv.QuoteRuneToASCII(r)))
		}
	}
	tokens = append(tokens, token{tEOF, "", len(lexer.expression), 0})
	return tokens, nil
}

func (lexer *Lexer) consumeUntil(end rune) (string, error) {
	start := lexer.currentPos
	current := lexer.next()
	for current != end && current != eof {
		if current == '\\' && lexer.peek() != eof {
			lexer.next()
		}
		current = lexer.next()
	}
	if lexer.lastWidth == 0 {
		return "", SyntaxError{
			msg:        "Unclosed delimiter: " + string(end),
			Expression: lexer.expression,
			Offset:     len(lexer.expression),
		}
	}
	return lexer.expression[start : lexer.currentPos-lexer.lastWidth], nil
}

func (lexer *Lexer) consumeLiteral() (token, error) {
	start := lexer.currentPos
	value, err := lexer.consumeUntil('`')
	if err != nil {
		return token{}, err
	}
	value = strings.Replace(value, "\\`", "`", -1)
	return token{
		tokenType: tJSONLiteral,
		value:     value,
		position:  start,
		length:    len(value),
	}, nil
}

func (lexer *Lexer) consumeRawStringLiteral() (token, error) {
	start := lexer.currentPos
	currentIndex := start
	current := lexer.next()
	for current != '\'' && lexer.peek() != eof {
		if current == '\\' && lexer.peek() == '\'' {
			chunk := lexer.expression[currentIndex : lexer.currentPos-1]
			lexer.buf.WriteString(chunk)
			lexer.buf.WriteString("'")
			lexer.next()
			currentIndex = lexer.currentPos
		}
		current = lexer.next()
	}
	if lexer.lastWidth == 0 {
		return token{}, SyntaxError{
			msg:        "Unclosed delimiter: '",
			Expression: lexer.expression,
			Offset:     len(lexer.expression),
		}
	}
	if currentIndex < lexer.currentPos {
		lexer.buf.WriteString(lexer.expression[currentIndex : lexer.currentPos-1])
	}
	value := lexer.buf.String()
	lexer.buf.Reset()
	return token{
		tokenType: tStringLiteral,
		value:     value,
		position:  start,
		length:    len(value),
	}, nil
}

func (lexer *Lexer) syntaxError(msg string) SyntaxError {
	return SyntaxError{
		msg:        msg,
		Expression: lexer.expression,
		Offset:     lexer.currentPos - 1,
	}
}

func (lexer *Lexer) matchOrElse(first rune, second rune, matchedType tokType, singleCharType tokType) token {
	start := lexer.currentPos - lexer.lastWidth
	nextRune := lexer.next()
	var t token
	if nextRune == second {
		t = token{
			tokenType: matchedType,
			value:     string(first) + string(second),
			position:  start,
			length:    2,
		}
	} else {
		lexer.back()
		t = token{
			tokenType: singleCharType,
			value:     string(first),
			position:  start,
			length:    1,
		}
	}
	return t
}

func (lexer *Lexer) consumeLBracket() token {
	start := lexer.currentPos - lexer.lastWidth
	nextRune := lexer.next()
	var t token
	if nextRune == '?' {
		t = token{
			tokenType: tFilter,
			value:     "[?",
			position:  start,
			length:    2,
		}
	} else if nextRune == ']' {
		t = token{
			tokenType: tFlatten,
			value:     "[]",
			position:  start,
			length:    2,
		}
	} else {
		t = token{
			tokenType: tLbracket,
			value:     "[",
			position:  start,
			length:    1,
		}
		lexer.back()
	}
	return t
}

func (lexer *Lexer) consumeQuotedIdentifier() (token, error) {
	start := lexer.currentPos
	value, err := lexer.consumeUntil('"')
	if err != nil {
		return token{}, err
	}
	var decoded string
	asJSON := []byte("\"" + value + "\"")
	if err := json.Unmarshal([]byte(asJSON), &decoded); err != nil {
		return token{}, err
	}
	return token{
		tokenType: tQuotedIdentifier,
		value:     decoded,
		position:  start - 1,
		length:    len(decoded),
	}, nil
}

func (lexer *Lexer) consumeUnquotedIdentifier() token {
	start := lexer.currentPos - lexer.lastWidth
	for {
		r := lexer.next()
		if r < 0 || r > 128 || identifierTrailingBits[uint64(r)/64]&(1<<(uint64(r)%64)) == 0 {
			lexer.back()
			break
		}
	}
	value := lexer.expression[start:lexer.currentPos]
	return token{
		tokenType: tUnquotedIdentifier,
		value:     value,
		position:  start,
		length:    lexer.currentPos - start,
	}
}

func (lexer *Lexer) consumeNumber() token {
	start := lexer.currentPos - lexer.lastWidth
	for {
		r := lexer.next()
		if r < '0' || r > '9' {
			lexer.back()
			break
		}
	}
	value := lexer.expression[start:lexer.currentPos]
	return token{
		tokenType: tNumber,
		value:     value,
		position:  start,
		length:    lexer.currentPos - start,
	}
}
