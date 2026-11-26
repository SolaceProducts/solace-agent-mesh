// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tmpl

import "embed"

//go:embed all:files
var files embed.FS

func Load(name string) (string, error) {
	data, err := files.ReadFile("files/" + name)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func MustLoad(name string) string {
	s, err := Load(name)
	if err != nil {
		panic("template: " + err.Error())
	}
	return s
}
