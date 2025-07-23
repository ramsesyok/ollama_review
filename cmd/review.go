/*
Copyright © 2025 ramsesyok

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
*/
package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/ollama/ollama/api"
	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/cpp"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/spf13/viper"
)

// langConfig associates file extensions with tree-sitter configuration.
var langConfig = map[string]struct {
	lang     *sitter.Language
	nodeType string
}{
	".py":   {python.GetLanguage(), "function_definition"},
	".java": {java.GetLanguage(), "method_declaration"},
	".cpp":  {cpp.GetLanguage(), "function_definition"},
	".hpp":  {cpp.GetLanguage(), "function_definition"},
	".h":    {cpp.GetLanguage(), "function_definition"},
	".go":   {golang.GetLanguage(), "function_declaration"},
}

// extractFunctions parses the given source and returns each function body.
func extractFunctions(src []byte, lang *sitter.Language, nodeType string) ([][]byte, error) {
	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(lang)
	tree, err := parser.ParseCtx(context.Background(), nil, src)
	if err != nil {
		return nil, fmt.Errorf("parse source: %w", err)
	}

	root := tree.RootNode()
	var chunks [][]byte
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n.Type() == nodeType {
			chunks = append(chunks, src[n.StartByte():n.EndByte()])
		}
		for c := n.NamedChild(0); c != nil; c = c.NextNamedSibling() {
			walk(c)
		}
	}
	walk(root)
	return chunks, nil
}

// buildPrompt generates the prompt from the guideline template.
func buildPrompt(tmplPath, lang string, code []byte) (string, error) {
	tmpl, err := template.ParseFiles(tmplPath)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}

	data := map[string]string{
		"lang": lang,
		"code": string(code),
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	return buf.String(), nil
}

// Ollama にレビュー依頼
func reviewChunk(client *api.Client, model string, guideline string, lang string, code []byte) (string, error) {
	prompt, err := buildPrompt(guideline, lang, code)
	if err != nil {
		return "", err
	}

	// Chat リクエストを作成
	req := &api.ChatRequest{
		Model:    model,
		Messages: []api.Message{{Role: "user", Content: prompt}},
	}

	var outBuf bytes.Buffer
	// 非ストリーミングで取得
	err = client.Chat(context.Background(), req, func(resp api.ChatResponse) error {
		outBuf.WriteString(resp.Message.Content)
		return nil
	})
	if err != nil {
		return "", err
	}
	return outBuf.String(), nil
}

func Review(repoRoot string, outFile string) error {
	rootDir := "."
	model := viper.GetString("model")
	ignoreDirs := map[string]struct{}{}
	for _, n := range viper.GetStringSlice("exclude") {
		ignoreDirs[n] = struct{}{}
	}
	guidelinePath := viper.GetString("guideline")

	var report []string
	walkFn := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err // パーミッションエラー等
		}
		if d.IsDir() {
			if _, ok := ignoreDirs[d.Name()]; ok && path != rootDir {
				// このディレクトリ配下は潜らない
				return fs.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		cfg, ok := langConfig[ext]
		if !ok {
			return nil
		}
		// ソース読み込み
		src, err := os.ReadFile(path)
		if err != nil {
			log.Printf("Read error %s: %v", path, err)
			return nil
		}
		// 関数チャンク抽出（Tree‑sitter）:contentReference[oaicite:1]{index=1}
		chunks, err := extractFunctions(src, cfg.lang, cfg.nodeType)
		if err != nil {
			log.Printf("Parse error %s: %v", path, err)
			return nil
		}
		if len(chunks) == 0 {
			return nil
		}

		// 2. URL をパース
		baseURL, err := url.Parse(viper.GetString("OllamaHost"))
		if err != nil {
			return fmt.Errorf("parse OLLAMA_HOST: %w", err)
		}

		client := api.NewClient(baseURL, http.DefaultClient)

		// ファイルごとにチャンクをレビュー
		for i, chunk := range chunks {
			res, err := reviewChunk(client, model, guidelinePath, strings.TrimPrefix(ext, "."), chunk)
			if err != nil {
				log.Printf("Review error %s[%d]: %v", path, i+1, err)
				continue
			}
			fmt.Println(res)
			report = append(report,
				fmt.Sprintf("## %s (chunk %d/%d)\n\n%s\n\n---\n",
					path, i+1, len(chunks), res))
		}
		return nil
	}
	if err := filepath.WalkDir(repoRoot, walkFn); err != nil {
		return err
	}
	// レポートを Markdown ファイルに出力
	if err := os.WriteFile(outFile,
		[]byte("# Code Review Report\n\n"+strings.Join(report, "")),
		0644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	fmt.Printf("Review completed: %s\n", outFile)
	return nil
}
