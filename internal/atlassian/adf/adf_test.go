package adf

import "testing"

func TestMarkdown(t *testing.T) {
	tests := []struct {
		name, doc, want string
	}{
		{"marks hug the text, spaces stay outside",
			`[{"type":"paragraph","content":[{"type":"text","text":"Status:","marks":[{"type":"strong"}]},
			  {"type":"text","text":" draft "},{"type":"text","text":"pull ","marks":[{"type":"em"}]},
			  {"type":"text","text":"x` + "`" + `y","marks":[{"type":"code"}]},{"type":"text","text":" and "},
			  {"type":"text","text":"gone","marks":[{"type":"strike"}]}]}]`,
			"**Status:** draft *pull* ``x`y`` and ~~gone~~"},
		{"links, and adjacent runs with the same look merge",
			`[{"type":"paragraph","content":[{"type":"text","text":"Discovery","marks":[{"type":"link","attrs":{"href":"https://x/1"}}]},
			  {"type":"text","text":" Brief","marks":[{"type":"link","attrs":{"href":"https://x/1"}},{"type":"textColor","attrs":{"color":"#f00"}}]},
			  {"type":"text","text":" · "},{"type":"text","text":"bold","marks":[{"type":"strong"},{"type":"link","attrs":{"href":"https://x/2"}}]}]}]`,
			"[Discovery Brief](https://x/1) · [**bold**](https://x/2)"},
		{"headings and hard breaks",
			`[{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Context"}]},
			  {"type":"heading","attrs":{"level":3}},
			  {"type":"paragraph","content":[{"type":"text","text":"In:"},{"type":"hardBreak"},{"type":"text","text":"Out:"}]}]`,
			"## Context\n\nIn:\nOut:"},
		{"nested lists, tight, with an ordered start",
			`[{"type":"bulletList","content":[
			   {"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"one"}]},
			     {"type":"orderedList","attrs":{"order":3},"content":[
			       {"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"three"}]}]},
			       {"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"four"}]}]}]}]},
			   {"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"two"}]}]}]}]`,
			"- one\n  3. three\n  4. four\n- two"},
		{"task and decision lists",
			`[{"type":"taskList","content":[
			   {"type":"taskItem","attrs":{"state":"DONE"},"content":[{"type":"text","text":"ship"}]},
			   {"type":"taskList","content":[{"type":"taskItem","attrs":{"state":"TODO"},"content":[{"type":"text","text":"docs"}]}]}]},
			  {"type":"decisionList","content":[{"type":"decisionItem","content":[{"type":"text","text":"push, not pull"}]}]}]`,
			"- [x] ship\n  - [ ] docs\n\n- Decision: push, not pull"},
		{"code block fences outgrow the content",
			"[{\"type\":\"codeBlock\",\"attrs\":{\"language\":\"go\"},\"content\":[{\"type\":\"text\",\"text\":\"x := \\\"```\\\"\\n\"}]}]",
			"````go\nx := \"```\"\n````"},
		{"table: first row is the header, cells flatten, pipes escape",
			`[{"type":"table","content":[
			   {"type":"tableRow","content":[{"type":"tableHeader","content":[{"type":"paragraph"}]},
			     {"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"PoC"}]}]}]},
			   {"type":"tableRow","content":[{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"a|b"}]}]},
			     {"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"x"}]},{"type":"paragraph","content":[{"type":"text","text":"y"}]}]},
			     {"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"extra"}]}]}]}]}]`,
			"|  | PoC |  |\n| --- | --- | --- |\n| a\\|b | x<br>y | extra |"},
		{"panels, quotes, expands and rules",
			`[{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"text","text":"careful"}]}]},
			  {"type":"blockquote","content":[{"type":"paragraph","content":[{"type":"text","text":"a"}]},{"type":"paragraph","content":[{"type":"text","text":"b"}]}]},
			  {"type":"expand","attrs":{"title":"More"},"content":[{"type":"paragraph","content":[{"type":"text","text":"hidden"}]}]},
			  {"type":"rule"}]`,
			"> [!WARNING]\n> careful\n\n> a\n>\n> b\n\n<details>\n<summary>More</summary>\n\nhidden\n\n</details>\n\n---"},
		{"inline nodes",
			`[{"type":"paragraph","content":[{"type":"mention","attrs":{"id":"abc","text":"@Jane Doe"}},{"type":"text","text":" "},
			  {"type":"mention","attrs":{"id":"def","text":""}},{"type":"text","text":" "},
			  {"type":"emoji","attrs":{"shortName":":tada:","text":"🎉"}},{"type":"emoji","attrs":{"shortName":":custom:"}},{"type":"text","text":" "},
			  {"type":"date","attrs":{"timestamp":"1704067200000"}},{"type":"text","text":" "},
			  {"type":"status","attrs":{"text":"IN REVIEW","color":"blue"}},{"type":"text","text":" "},
			  {"type":"inlineCard","attrs":{"url":"https://x.atlassian.net/browse/ENG-1"}},
			  {"type":"placeholder","attrs":{"text":"Type here"}}]}]`,
			"@Jane Doe @def 🎉:custom: 2024-01-01 [IN REVIEW] https://x.atlassian.net/browse/ENG-1"},
		{"media and cards",
			`[{"type":"mediaSingle","content":[{"type":"media","attrs":{"id":"m-1","type":"file","alt":"diagram.png"}},
			    {"type":"caption","content":[{"type":"text","text":"The flow"}]}]},
			  {"type":"mediaGroup","content":[{"type":"media","attrs":{"type":"external","url":"https://img/x.png"}}]},
			  {"type":"blockCard","attrs":{"data":{"url":"https://example.com/doc"}}}]`,
			"![diagram.png](media:m-1)\nThe flow\n\n![attachment](https://img/x.png)\n\nhttps://example.com/doc"},
		{"macros show their parameters; bodied ones their content",
			`[{"type":"extension","attrs":{"extensionType":"com.atlassian.confluence.macro.core","extensionKey":"jira",
			    "parameters":{"macroParams":{"key":{"value":"ENG-7"},"_internal":{"value":"x"},"columns":{"value":"key,summary"}}}}},
			  {"type":"extension","attrs":{"extensionKey":"toc"}},
			  {"type":"bodiedExtension","attrs":{"extensionKey":"excerpt"},"content":[{"type":"paragraph","content":[{"type":"text","text":"summary"}]}]},
			  {"type":"layoutSection","content":[{"type":"layoutColumn","content":[{"type":"paragraph","content":[{"type":"text","text":"left"}]}]},
			    {"type":"layoutColumn","content":[{"type":"paragraph","content":[{"type":"text","text":"right"}]}]}]}]`,
			"[jira: columns=key,summary, key=ENG-7]\n\n[toc]\n\nsummary\n\nleft\n\nright"},
		{"unknown nodes keep their text; stray inlines become a paragraph",
			`[{"type":"futureBlock","content":[{"type":"paragraph","content":[{"type":"text","text":"kept"}]}]},
			  {"type":"text","text":"stray"},{"type":"paragraph"},{"type":"paragraph","content":[{"type":"text","text":"  "}]}]`,
			"kept\n\nstray"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Markdown([]byte(`{"type":"doc","version":1,"content":` + tt.doc + `}`))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got:\n%s\n\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestIsDoc(t *testing.T) {
	doc := map[string]any{"type": "doc", "version": float64(1), "content": []any{}}
	if !IsDoc(doc) || IsDoc(map[string]any{"type": "doc"}) || IsDoc("doc") || IsDoc(map[string]any{"value": "x"}) {
		t.Error("IsDoc should accept only {type: doc, version, content: [...]}")
	}
	if _, err := Markdown([]byte(`not json`)); err == nil {
		t.Error("want an error for invalid JSON")
	}
}
