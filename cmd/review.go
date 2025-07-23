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

// このファイルでは Tree-sitter で関数を抽出し、Ollama にコードレビュー
// を依頼するためのユーティリティ関数群を提供する。

// langConfig では拡張子ごとに Tree-sitter の設定を定義する。
// lang は解析に用いる言語定義、nodeType は関数ノードの種類を表す。
// 対応言語を追加する際はここへ設定を追記するだけでよい。
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

// extractFunctions は Tree-sitter を利用してソースから関数ブロックのみを
// 抽出するヘルパー。言語定義とノード種別を受け取り、再帰的に構文木を探索
// して対象ノードのコード片を返す。
func extractFunctions(src []byte, lang *sitter.Language, nodeType string) ([][]byte, error) {
	parser := sitter.NewParser() // パーサ生成
	defer parser.Close()
	parser.SetLanguage(lang) // 解析対象の言語を設定

	// ソースコードをパースして構文木を取得
	tree, err := parser.ParseCtx(context.Background(), nil, src)
	if err != nil {
		return nil, fmt.Errorf("parse source: %w", err)
	}

	root := tree.RootNode()
	var chunks [][]byte
	// DFS でノードを走査し関数ノードを収集
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n.Type() == nodeType {
			// 関数ノードに該当したらコード片を切り出す
			chunks = append(chunks, src[n.StartByte():n.EndByte()])
		}
		for c := n.NamedChild(0); c != nil; c = c.NextNamedSibling() {
			walk(c)
		}
	}
	walk(root)
	return chunks, nil
}

// buildPrompt はテンプレートファイルを読み込み、言語名とコードを埋め込んだ
// プロンプト文字列を生成する。
func buildPrompt(tmplPath, lang string, code []byte) (string, error) {
	// テンプレートをパース
	tmpl, err := template.ParseFiles(tmplPath)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}

	// テンプレートに渡すデータ
	data := map[string]string{
		"lang": lang,
		"code": string(code),
	}

	// 実行して結果をバッファへ書き出す
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	return buf.String(), nil
}

// reviewChunk は 1 つのチャンクを Ollama に送信し、レビュー結果を取得する
// ヘルパー関数。
func reviewChunk(client *api.Client, model string, guideline string, lang string, code []byte) (string, error) {
	// プロンプトの生成
	prompt, err := buildPrompt(guideline, lang, code)
	if err != nil {
		return "", err
	}

	// Ollama API へ送るチャットリクエストを準備
	req := &api.ChatRequest{
		Model:    model,
		Messages: []api.Message{{Role: "user", Content: prompt}},
	}

	var outBuf bytes.Buffer
	// ストリームをまとめてバッファに蓄積する
	err = client.Chat(context.Background(), req, func(resp api.ChatResponse) error {
		outBuf.WriteString(resp.Message.Content)
		return nil
	})
	if err != nil {
		return "", err
	}
	return outBuf.String(), nil
}

// Review はリポジトリ内を探索し、各ファイルの関数単位で AI にレビューを
// 依頼するメイン関数。取得した結果は Markdown として保存される。
func Review(repoRoot string, outFile string) error {
	log.Printf("Start review: repo=%s", repoRoot)
	rootDir := "." // WalkDir の起点

	// 使用するモデル名を設定ファイルから取得
	model := viper.GetString("model")

	// 除外ディレクトリをマップ化して高速に判定
	ignoreDirs := map[string]struct{}{}
	for _, n := range viper.GetStringSlice("exclude") {
		ignoreDirs[n] = struct{}{}
	}

	guidelinePath := viper.GetString("guideline") // ガイドラインテンプレート

	// レビュー結果を格納するスライス
	var report []string

	// WalkDir に渡すコールバック
	walkFn := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// パーミッションエラー等が発生した場合はそのまま返す
			return err
		}
		if d.IsDir() {
			if _, ok := ignoreDirs[d.Name()]; ok && path != rootDir {
				// 指定されたディレクトリは探索しない
				return fs.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		cfg, ok := langConfig[ext]
		if !ok {
			return nil
		}
		log.Printf("Processing %s", path)
		// 対象ファイルを読み込む
		src, err := os.ReadFile(path)
		if err != nil {
			log.Printf("Read error %s: %v", path, err)
			return nil
		}
		// Tree-sitter により関数チャンクを抽出
		chunks, err := extractFunctions(src, cfg.lang, cfg.nodeType)
		if err != nil {
			log.Printf("Parse error %s: %v", path, err)
			return nil
		}
		if len(chunks) == 0 {
			log.Printf("No functions found in %s", path)
			return nil
		}

		// Ollama サーバーの URL を取得
		baseURL, err := url.Parse(viper.GetString("OllamaHost"))
		if err != nil {
			return fmt.Errorf("parse OLLAMA_HOST: %w", err)
		}

		client := api.NewClient(baseURL, http.DefaultClient)

		// 抽出したチャンクを順番にレビュー
		for i, chunk := range chunks {
			res, err := reviewChunk(client, model, guidelinePath, strings.TrimPrefix(ext, "."), chunk)
			if err != nil {
				log.Printf("Review error %s[%d]: %v", path, i+1, err)
				continue
			}
			log.Printf("%s chunk %d/%d reviewed", path, i+1, len(chunks))
			log.Println(res)
			report = append(report,
				fmt.Sprintf("## %s (chunk %d/%d)\n\n%s\n\n---\n",
					path, i+1, len(chunks), res))
		}
		return nil
	}
	if err := filepath.WalkDir(repoRoot, walkFn); err != nil {
		return err
	}
	// まとめたレポートを Markdown ファイルへ出力
	if err := os.WriteFile(outFile,
		[]byte("# Code Review Report\n\n"+strings.Join(report, "")),
		0644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	log.Printf("Report saved: %s", outFile)
	log.Printf("Review completed: %s", outFile)
	return nil
}
