/*
Copyright (c) 2025 Atticus

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to use and distribute the Software, subject to the following conditions:

- The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.
- Credit must be given to Atticus in any distribution or use of this Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
*/

package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/segmentio/encoding/json"
)

var debug = true // only change if testing or if you like console logs for whatever reason

// HTTP client (shared) just makes requests faster
var sharedHTTPClient = &http.Client{
	Timeout: 60 * time.Second,
}

// ollamaReq is the request format for ollama
type ollamaReq struct {
	Model    string      `json:"model"`
	Messages []msg       `json:"messages"`
	Stream   bool        `json:"stream,omitempty"`
	Options  interface{} `json:"options,omitempty"`
}

// msg is the message format for ollama
type msg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatReq is the request format for pfuner.xyz
type chatReq struct {
	Messages []string `json:"messages"`
}

// chatResp is the response format for pfuner.xyz
type chatResp struct {
	Reply string `json:"reply"`
	Ms    int64  `json:"ms"`
}

// ollamaResp is the response format for ollama
type ollamaResp struct {
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	Message   msg    `json:"message"`
	Done      bool   `json:"done"`
}

// main function (starts the server)
func main() {
	http.HandleFunc("/api/chat", hChat)
	// import _ "net/http/pprof" at the top to enable
	// _ = http.ListenAndServe("localhost:6060", nil) // Uncomment to enable pprof on a separate port
	prt := ":11434"
	fmt.Printf("starting server on http://127.0.0.1%s\n", prt)
	fmt.Println("please make sure to close ollama before continuing")
	fmt.Println("all requests with invalid models be redirected to pfuner.xyz/v1/chat/completions (AKA GPT-3.5)")
	log.Fatal(http.ListenAndServe(prt, nil))
}

// handler for requests to /api/chat :D
func hChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req ollamaReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	model := req.Model
	var endpoint string
	var reqBody []byte
	contentType := "application/json"
	isChatStream := false
	isV2 := false
	switch model {
	case "gpt-4o", "gpt-4o-mini", "gpt-4.1-nano", "gpt-4.1-mini", "gpt-4.1":
		endpoint = "https://pfuner.xyz/v2/chat/completions"
		temp := 0.7
		if opts, ok := req.Options.(map[string]interface{}); ok {
			if t, ok := opts["temperature"].(float64); ok {
				temp = t
			}
		}
		var openaiMsgs []map[string]interface{}
		for _, m := range req.Messages {
			openaiMsgs = append(openaiMsgs, map[string]interface{}{
				"role":    m.Role,
				"content": m.Content,
			})
		}
		uhhobjofchatReq := map[string]interface{}{
			"model":       model,
			"messages":    openaiMsgs,
			"temperature": temp,
		}
		reqBody, _ = json.Marshal(uhhobjofchatReq)
		if debug {
			fmt.Println("[DEBUG] Sending to pfuner.xyz/v2/chat/completions:", string(reqBody))
		}
		isChatStream = true
		isV2 = true
	case "dall-e-3":
		endpoint = "https://pfuner.xyz/v3/images/generations"
		prompt := ""
		if len(req.Messages) > 0 {
			prompt = req.Messages[len(req.Messages)-1].Content
		}
		imgReq := map[string]interface{}{
			"model":  "dall-e-3",
			"prompt": prompt,
			"size":   "1024x1024",
			"n":      1,
		}
		reqBody, _ = json.Marshal(imgReq)
		if debug {
			fmt.Println("[DEBUG] Sending to pfuner.xyz/v3/images/generations:", string(reqBody))
		}
	case "base64":
		endpoint = "https://pfuner.xyz/v4/images/generations"
		prompt := ""
		if len(req.Messages) > 0 {
			prompt = req.Messages[len(req.Messages)-1].Content
		}
		imgReq := map[string]interface{}{
			"prompt": prompt,
		}
		reqBody, _ = json.Marshal(imgReq)
	case "tts":
		endpoint = "https://pfuner.xyz/v5/audio/generations"
		text := ""
		if len(req.Messages) > 0 {
			text = req.Messages[len(req.Messages)-1].Content
		}
		ttsReq := map[string]interface{}{
			"text": text,
		}
		reqBody, _ = json.Marshal(ttsReq)
	default:
		endpoint = "https://pfuner.xyz/v1/chat/completions"
		var messages []string
		for _, m := range req.Messages {
			messages = append(messages, m.Content)
		}
		chatReq := chatReq{
			Messages: messages,
		}
		reqBody, _ = json.Marshal(chatReq)
		isChatStream = true
	}
	resp, err := sharedHTTPClient.Post(endpoint, contentType, bytes.NewBuffer(reqBody))
	if err != nil {
		http.Error(w, "[ERROR] forwarding request...", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "[ERROR] reading response...", http.StatusInternalServerError)
		return
	}
	if resp.StatusCode == 429 || strings.Contains(string(body), "\"Too many requests (\"") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		ollamaErrResp := ollamaResp{
			Model:     model,
			CreatedAt: nowRFC(),
			Message: msg{
				Role:    "assistant",
				Content: "Too many requests please wait a min... (contact atticus if you think higher request limits should be set)",
			},
			Done: true,
		}
		respBytes, _ := json.Marshal(ollamaErrResp)
		w.Write(respBytes)
		w.Write([]byte("\n"))
		return
	}
	if debug {
		fmt.Println("[DEBUG] pfuner.xyz replied:", string(body))
	}
	createdAt := nowRFC()
	if isChatStream {
		reply := ""
		if isV2 {
			var v2 struct {
				Content string `json:"content"`
				Ms      int64  `json:"ms"`
			}
			if err := json.Unmarshal(body, &v2); err != nil {
				http.Error(w, "[ERROR] parsing v2 response...", http.StatusInternalServerError)
				return
			}
			reply = v2.Content
		} else {
			var uhhchatresp chatResp
			if err := json.Unmarshal(body, &uhhchatresp); err != nil {
				http.Error(w, "[ERROR] parsing response...", http.StatusInternalServerError)
				return
			}
			reply = uhhchatresp.Reply
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "unsupported stream...", http.StatusInternalServerError)
			return
		}
		words := SplitW(reply)
		uuhhnoideahowtocallthislol := 100 //i recommend not to touch if number too big/small could hunder performance
		for i := 0; i < len(words); i += uuhhnoideahowtocallthislol {
			end := i + uuhhnoideahowtocallthislol
			if end > len(words) {
				end = len(words)
			}
			var builder strings.Builder
			for _, w := range words[i:end] {
				builder.WriteString(w)
			}
			content := builder.String()
			done := end == len(words)
			uhhobjofollamaResp := ollamaResp{
				Model:     model,
				CreatedAt: createdAt,
				Message: msg{
					Role:    "assistant",
					Content: content,
				},
				Done: done,
			}
			respBytes, _ := json.Marshal(uhhobjofollamaResp)
			w.Write(respBytes)
			w.Write([]byte("\n"))
			flusher.Flush()
		}
		return
	}
	if model == "dall-e-3" {
		var imgResp struct {
			Created int64 `json:"created"`
			Data    []struct {
				RevisedPrompt string `json:"revised_prompt"`
				URL           string `json:"url"`
			} `json:"data"`
			Ms int64 `json:"ms"`
		}
		if err := json.Unmarshal(body, &imgResp); err != nil {
			http.Error(w, "[ERROR] generating image (parsing the response)...", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "unsupported stream...", http.StatusInternalServerError)
			return
		}
		imageURL := ""
		if len(imgResp.Data) > 0 {
			imageURL = imgResp.Data[0].URL
		}
		uhhobjofollamaResp := ollamaResp{
			Model:     model,
			CreatedAt: createdAt,
			Message: msg{
				Role:    "assistant",
				Content: imageURL,
			},
			Done: true,
		}
		respBytes, _ := json.Marshal(uhhobjofollamaResp)
		w.Write(respBytes)
		w.Write([]byte("\n"))
		flusher.Flush()
		return
	}
	if model == "base64" {
		var base64Resp struct {
			Output [][]string `json:"output"`
			Ms     int64      `json:"ms"`
		}
		if err := json.Unmarshal(body, &base64Resp); err != nil {
			http.Error(w, "[ERROR] generating base64...", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "unsupported stream...", http.StatusInternalServerError)
			return
		}
		base64str := ""
		if len(base64Resp.Output) > 0 && len(base64Resp.Output[0]) > 0 {
			base64str = base64Resp.Output[0][0]
		}
		uhhobjofollamaResp := ollamaResp{
			Model:     model,
			CreatedAt: createdAt,
			Message: msg{
				Role:    "assistant",
				Content: base64str,
			},
			Done: true,
		}
		respBytes, _ := json.Marshal(uhhobjofollamaResp)
		w.Write(respBytes)
		w.Write([]byte("\n"))
		flusher.Flush()
		return
	}
	if model == "tts" {
		var ttsResp struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(body, &ttsResp); err != nil {
			http.Error(w, "[ERROR] generating tts...", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "unsupported stream...", http.StatusInternalServerError)
			return
		}
		uhhobjofollamaResp := ollamaResp{
			Model:     model,
			CreatedAt: createdAt,
			Message: msg{
				Role:    "assistant",
				Content: ttsResp.URL,
			},
			Done: true,
		}
		respBytes, _ := json.Marshal(uhhobjofollamaResp)
		w.Write(respBytes)
		w.Write([]byte("\n"))
		flusher.Flush()
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// split words (just so the responses are the same as ollama)
func SplitW(s string) []string {
	var result []string
	var current string
	pendingSpace := false

	for i, r := range s {
		switch r {
		case ' ':
			if current != "" {
				result = append(result, current)
				current = ""
			}
			pendingSpace = true
		case '\n', '\t':
			if current != "" {
				result = append(result, current)
				current = ""
			}
			if r == '\n' {
				result = append(result, "\n")
			} else {
				result = append(result, "\t")
			}
			pendingSpace = false
		default:
			if pendingSpace {
				current = " " + string(r)
				pendingSpace = false
			} else {
				current += string(r)
			}
		}
		// appends it if this is the last rune and current isn't empty 🫠
		if i == len(s)-1 && current != "" {
			result = append(result, current)
		}
	}
	return result
}

// same rfc timestamp as ollama
func nowRFC() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.0000000Z")
}
