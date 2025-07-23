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
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string
var repository string

// rootCmd はサブコマンドなしで実行された際の基点となるコマンド
var rootCmd = &cobra.Command{
	Use:   "ollama_review",
	Short: "Ollama を利用したコードレビュー CLI",
	Long: `Ollama と Tree-sitter を利用してソースコードを解析し、
AI によるレビュー結果を出力するツールです。`,
	Run: func(cmd *cobra.Command, args []string) {
		output := viper.GetString("output")
		Review(repository, output)
	},
}

// Execute は rootCmd にサブコマンドを登録して実行する
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// 設定ファイルのパスを指定するフラグ
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "config.yaml", "config file (default is config.yaml)")

	// レビュー対象リポジトリを指定するフラグ
	rootCmd.Flags().StringVarP(&repository, "repository", "r", "", "Select code review targets.")
}

// initConfig は設定ファイルと環境変数を読み込む
func initConfig() {
	if cfgFile != "" {
		// フラグで指定された設定ファイルを使用
		viper.SetConfigFile(cfgFile)
	} else {
		// 実行ファイルのディレクトリから設定ファイルを探索
		exec, err := os.Executable()
		cobra.CheckErr(err)
		exeDir := filepath.Dir(exec)

		// ".ollama_review" という名前の YAML を探す
		viper.AddConfigPath(exeDir)
		viper.SetConfigType("yaml")
		viper.SetConfigName(".ollama_review")
	}

	viper.AutomaticEnv() // 環境変数を自動的に読み込む

	// 設定ファイルが存在する場合は読み込む
	if err := viper.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
	}
}
