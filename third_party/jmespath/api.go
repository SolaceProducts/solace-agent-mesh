package jmespath

import "strconv"

type JMESPath struct {
	ast  ASTNode
	intr *treeInterpreter
}

func Compile(expression string) (*JMESPath, error) {
	parser := NewParser()
	ast, err := parser.Parse(expression)
	if err != nil {
		return nil, err
	}
	jmespath := &JMESPath{ast: ast, intr: newInterpreter()}
	return jmespath, nil
}

func MustCompile(expression string) *JMESPath {
	jmespath, err := Compile(expression)
	if err != nil {
		panic(`jmespath: Compile(` + strconv.Quote(expression) + `): ` + err.Error())
	}
	return jmespath
}

func (jp *JMESPath) Search(data interface{}) (interface{}, error) {
	return jp.intr.Execute(jp.ast, data)
}

func Search(expression string, data interface{}) (interface{}, error) {
	intr := newInterpreter()
	parser := NewParser()
	ast, err := parser.Parse(expression)
	if err != nil {
		return nil, err
	}
	return intr.Execute(ast, data)
}
