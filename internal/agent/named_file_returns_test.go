// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import "testing"

func TestNamedFileReturnsMatchesWholeFileNamesOnly(t *testing.T) {
	tests := map[string]struct {
		files  []string
		answer string
		want   []string
	}{
		"a name at the start of the answer":                          {[]string{"data.csv"}, "data.csv is ready", []string{"data.csv"}},
		"a name at the end of a sentence with a period":              {[]string{"data.csv"}, "The export is data.csv.", []string{"data.csv"}},
		"a name followed by a comma":                                 {[]string{"data.csv"}, "Saved data.csv, then stopped", []string{"data.csv"}},
		"a name in backticks":                                        {[]string{"data.csv"}, "The export is `data.csv`", []string{"data.csv"}},
		"a name in quotes":                                           {[]string{"data.csv"}, `Saved "data.csv" for you`, []string{"data.csv"}},
		"a name in curly quotes":                                     {[]string{"data.csv"}, "Saved “data.csv” for you", []string{"data.csv"}},
		"a name in parentheses":                                      {[]string{"data.csv"}, "The export (data.csv) is ready", []string{"data.csv"}},
		"a name in bold":                                             {[]string{"data.csv"}, "The export is **data.csv**", []string{"data.csv"}},
		"a name in a markdown link label":                            {[]string{"data.csv"}, "Open [data.csv](link)", []string{"data.csv"}},
		"a name after a longer name that contains it":                {[]string{"data.csv"}, "metadata.csv and data.csv", []string{"data.csv"}},
		"a name that ends a longer name":                             {[]string{"data.csv"}, "see metadata.csv", nil},
		"a name that starts a longer name":                           {[]string{"data.csv"}, "see data.csv.bak", nil},
		"a name that follows an underscore":                          {[]string{"data.csv"}, "see old_data.csv", nil},
		"a name that follows a dash":                                 {[]string{"data.csv"}, "see old-data.csv", nil},
		"a name that follows a folder":                               {[]string{"data.csv"}, "see exports/data.csv", nil},
		"a name between CJK characters":                              {[]string{"data.csv"}, "文件data.csv已生成", []string{"data.csv"}},
		"a name before an ideographic full stop":                     {[]string{"data.csv"}, "已生成 data.csv。", []string{"data.csv"}},
		"a name in full-width parentheses":                           {[]string{"data.csv"}, "（data.csv）", []string{"data.csv"}},
		"a name in CJK corner brackets":                              {[]string{"data.csv"}, "「data.csv」", []string{"data.csv"}},
		"a name after hiragana":                                      {[]string{"data.csv"}, "ふあいるdata.csv", []string{"data.csv"}},
		"a name after katakana":                                      {[]string{"data.csv"}, "サーバdata.csv", []string{"data.csv"}},
		"a name after the katakana prolonged sound mark":             {[]string{"data.csv"}, "ーdata.csv", []string{"data.csv"}},
		"a name before a hangul particle":                            {[]string{"data.csv"}, "data.csv를 보냈습니다", []string{"data.csv"}},
		"a name after hangul":                                        {[]string{"data.csv"}, "파일data.csv", []string{"data.csv"}},
		"a name after thai":                                          {[]string{"data.csv"}, "ไฟล์data.csv", []string{"data.csv"}},
		"a name after lao":                                           {[]string{"data.csv"}, "ໄຟລ໌data.csv", []string{"data.csv"}},
		"a name after khmer":                                         {[]string{"data.csv"}, "ឯកសារdata.csv", []string{"data.csv"}},
		"a name after myanmar":                                       {[]string{"data.csv"}, "ဖိုင်data.csv", []string{"data.csv"}},
		"a name after tibetan":                                       {[]string{"data.csv"}, "ཡིག་ཆdata.csv", []string{"data.csv"}},
		"a name next to full-width digits":                           {[]string{"data.csv"}, "２data.csv３", []string{"data.csv"}},
		"a name next to full-width letters":                          {[]string{"data.csv"}, "Ａdata.csvＺ", []string{"data.csv"}},
		"a name that follows a cyrillic letter":                      {[]string{"data.csv"}, "фdata.csv", nil},
		"a name that precedes a cyrillic letter":                     {[]string{"data.csv"}, "data.csvф", nil},
		"a name that follows an arabic letter":                       {[]string{"data.csv"}, "ملفdata.csv", nil},
		"a name that follows an arabic-indic digit":                  {[]string{"data.csv"}, "٣data.csv", nil},
		"a name that follows a latin letter with a combining accent": {[]string{"data.csv"}, "e\u0301data.csv", nil},
		"a name followed by a combining accent":                      {[]string{"data.csv"}, "data.csv\u0301", nil},
		"a name after a han character with a combining accent":       {[]string{"data.csv"}, "文\u0301data.csv", []string{"data.csv"}},
		"a name in curly single quotes":                              {[]string{"data.csv"}, "Saved ‘data.csv’ ok", []string{"data.csv"}},
		"a name between em dashes":                                   {[]string{"data.csv"}, "Saved—data.csv—now", []string{"data.csv"}},
		"a name before an ellipsis":                                  {[]string{"data.csv"}, "Saved data.csv…", []string{"data.csv"}},
		"a name in underscore emphasis":                              {[]string{"data.csv"}, "See _data.csv_ now", []string{"data.csv"}},
		"a name in underscore emphasis before a period":              {[]string{"data.csv"}, "See _data.csv_.", []string{"data.csv"}},
		"a name in double underscore emphasis":                       {[]string{"data.csv"}, "See __data.csv__ now", []string{"data.csv"}},
		"a name that follows an accented letter":                     {[]string{"data.csv"}, "see édata.csv", nil},
		"a name that follows a digit":                                {[]string{"data.csv"}, "see 2data.csv", nil},
		"a name that follows two underscores and a letter":           {[]string{"data.csv"}, "see old__data.csv", nil},
		"a name that follows an underscore-led word":                 {[]string{"data.csv"}, "see _old_data.csv", nil},
		"a name followed by an underscore and a letter":              {[]string{"data.csv"}, "see data.csv_v2", nil},
		"a name followed by a dash and a letter":                     {[]string{"data.csv"}, "see data.csv-old", nil},
		"a name followed by a dot and a letter":                      {[]string{"data.csv"}, "see data.csv.Next", nil},
		"a name followed by a folder separator":                      {[]string{"data.csv"}, "see data.csv/old", nil},
		"an extensionless name inside a longer word":                 {[]string{"report"}, "the reports are ready", nil},
		"an extensionless name as a word":                            {[]string{"report"}, "the report is ready", []string{"report"}},
		"a name already embedded":                                    {[]string{"data.csv"}, "«artifact_return:data.csv» is the export", nil},
		"a name already embedded with a version":                     {[]string{"data.csv"}, "«artifact_return:data.csv:2» is the export", nil},
		"a longer name embedded does not hide the short":             {[]string{"data.csv", "data.csv.bak"}, "«artifact_return:data.csv.bak» and data.csv", []string{"data.csv"}},
		"a longer name embedded first does not hide the exact embed": {[]string{"data.csv"}, "data.csv is ready «artifact_return:data.csv.bak» «artifact_return:data.csv»", nil},
		"two files named in one answer":                              {[]string{"a.csv", "b.csv"}, "a.csv and b.csv", []string{"a.csv", "b.csv"}},
	}
	for label, tc := range tests {
		t.Run(label, func(t *testing.T) {
			var want string
			for _, name := range tc.want {
				want += "\n\n«artifact_return:" + name + "»"
			}

			if got := namedFileReturns(tc.answer, tc.files); got != want {
				t.Fatalf("namedFileReturns(%q, %q) = %q, want %q", tc.answer, tc.files, got, want)
			}
		})
	}
}

func TestNamedFileReturnsLettersOfWordSpacedScriptsExtendTheName(t *testing.T) {
	letters := map[string]string{
		"Latin":               "\u00e9",
		"Greek":               "\u03b1",
		"Cyrillic":            "\u0444",
		"Coptic":              "\u2c81",
		"Armenian":            "\u0561",
		"Georgian":            "\u10d0",
		"Hebrew":              "\u05d0",
		"Arabic":              "\u0627",
		"Syriac":              "\u0710",
		"Thaana":              "\u0780",
		"Nko":                 "\u07ca",
		"Ethiopic":            "\u12a0",
		"Tifinagh":            "\u2d30",
		"Cherokee":            "\u13a0",
		"Canadian_Aboriginal": "\u1401",
		"Mongolian":           "\u1820",
		"Devanagari":          "\u0905",
		"Bengali":             "\u0985",
		"Gurmukhi":            "\u0a05",
		"Gujarati":            "\u0a85",
		"Oriya":               "\u0b05",
		"Tamil":               "\u0b85",
		"Telugu":              "\u0c05",
		"Kannada":             "\u0c85",
		"Malayalam":           "\u0d05",
		"Sinhala":             "\u0d85",
	}
	for script, letter := range letters {
		for _, answer := range []string{letter + "data.csv", "data.csv" + letter} {
			t.Run(script, func(t *testing.T) {
				if got := namedFileReturns(answer, []string{"data.csv"}); got != "" {
					t.Fatalf("namedFileReturns(%q) = %q, want no embed", answer, got)
				}
			})
		}
	}
}
