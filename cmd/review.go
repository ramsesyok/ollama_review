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

// 関数ノードを抽出
func extractFunctions(src []byte, lang *sitter.Language, nodeType string) [][]byte {
	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(lang)
	tree, err := parser.ParseCtx(context.Background(), nil, src)
	if err != nil {
		return [][]byte{}
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
	return chunks
}

// Ollama にレビュー依頼
func reviewChunk(client *api.Client, model string, guideline, lang string, code []byte) (string, error) {

	writer := new(strings.Builder)
	if tmpl, err := template.New(guideline).ParseFiles(guideline); err != nil {
		return "", err
	} else {
		variable := map[string]string{"lang": lang, "code": string(code)}
		if err := tmpl.Execute(writer, variable); err != nil {
			return "", err
		}
	}
	prompt := writer.String()

	// Chat リクエストを作成
	req := &api.ChatRequest{
		Model:    model,
		Messages: []api.Message{{Role: "user", Content: prompt}},
	}

	var outBuf bytes.Buffer
	// 非ストリーミングで取得
	err := client.Chat(context.Background(), req, func(resp api.ChatResponse) error {
		outBuf.WriteString(resp.Message.Content)
		return nil
	})
	return outBuf.String(), err
}

func Review(repoRoot string, outFile string) {

	root := "."
	model := viper.GetString("model")
	ignoreNames := map[string]struct{}{}
	exclude := viper.GetStringSlice("exclude")
	guideline := viper.GetString("guideline")
	for _, n := range exclude {
		ignoreNames[n] = struct{}{}
	}
	var report []string
	// リポジトリ内を再帰走査
	filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err // パーミッションエラー等
		}
		if d.IsDir() {
			if _, ok := ignoreNames[d.Name()]; ok && path != root {
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
		chunks := extractFunctions(src, cfg.lang, cfg.nodeType)
		if len(chunks) == 0 {
			return nil
		}

		// 2. URL をパース
		baseURL, err := url.Parse(viper.GetString("OllamaHost"))
		if err != nil {
			log.Fatalf("OLLAMA_HOST の URL パースエラー: %v", err)
		}

		// 3. NewClient で環境変数に頼らずクライアント生成 :contentReference[oaicite:0]{index=0}
		client := api.NewClient(baseURL, http.DefaultClient)

		// ファイルごとにチャンクをレビュー
		for i, chunk := range chunks {
			res, err := reviewChunk(client, model, guideline, strings.TrimPrefix(ext, "."), chunk)
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
	})

	// レポートを Markdown ファイルに出力
	err := os.WriteFile(outFile,
		[]byte("# Code Review Report\n\n"+strings.Join(report, "")),
		0644)
	if err != nil {
		log.Fatalf("Failed to write report: %v", err)
	}

	fmt.Printf("Review completed: %s\n", outFile)
}
