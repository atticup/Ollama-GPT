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

// Global stream override: nil = per-request, true = always stream, false = never stream
var streamOverride *bool

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
	Model              string `json:"model"`
	CreatedAt          string `json:"created_at"`
	Message            msg    `json:"message"`
	DoneReason         string `json:"done_reason,omitempty"`
	Done               bool   `json:"done"`
	TotalDuration      int64  `json:"total_duration,omitempty"`
	LoadDuration       int64  `json:"load_duration,omitempty"`
	PromptEvalCount    int    `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64  `json:"prompt_eval_duration,omitempty"`
	EvalCount          int    `json:"eval_count,omitempty"`
	EvalDuration       int64  `json:"eval_duration,omitempty"`
}

// main function (starts the server)
func main() {
	var input string
	inputCh := make(chan string, 1)
	go func() {
		fmt.Print("Force streaming? (on/off/ask): ")
		fmt.Scanln(&input)
		inputCh <- input
	}()
	select {
	case input = <-inputCh:
		input = strings.ToLower(strings.TrimSpace(input))
		if input == "on" {
			b := true
			streamOverride = &b
			fmt.Println("Streaming will always be ON for this session")
		} else if input == "off" {
			b := false
			streamOverride = &b
			fmt.Println("Streaming will always be OFF for this session")
		} else {
			streamOverride = nil
			fmt.Println("Streaming will be decided per request of the service")
		}
	case <-time.After(10 * time.Second):
		streamOverride = nil
		fmt.Println("\nno input in 10s defaulting to ask (basically the service decides) mode.")
	}
	http.HandleFunc("/api/chat", hChat)
	http.HandleFunc("/api/generate", hChat)
	http.HandleFunc("/api/tags", hTags)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Ollama is running")) //spoofs the fact that ollama is running cuz some services relay on it
	})
	prt := ":11434"
	fmt.Printf("starting server on http://127.0.0.1%s\n", prt)
	fmt.Println("please make sure to close ollama before continuing")
	fmt.Println("all requests with invalid models be redirected to pfuner.xyz/v1/chat/completions (AKA GPT-3.5)")
	log.Fatal(http.ListenAndServe(prt, nil))
}

// handler for requests to /api/chat and /api/generate :D
func hChat(w http.ResponseWriter, r *http.Request) {
	// allows all cors cuz some apps require them
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

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
			DoneReason: "stop",
			Done:       true,
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
		// global override to prevent service from changing it
		stream := req.Stream
		if streamOverride != nil {
			stream = *streamOverride
		}
		if stream {
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.WriteHeader(http.StatusOK)
			// Remove all U+000A (Line Feed) characters from reply
			reply = strings.ReplaceAll(reply, "\n", "")
			cleaned := make([]rune, 0, len(reply))
			for _, r := range reply {
				if r == 0x20 || (r >= 0x21 && r <= 0x7E) {
					cleaned = append(cleaned, r)
				}
			}
			reply = string(cleaned)
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "unsupported stream...", http.StatusInternalServerError)
				return
			}
			words := strings.Fields(reply)
			pos := 0
			for _, word := range words {
				start := strings.Index(reply[pos:], word)
				if start == -1 {
					continue
				}
				start += pos
				chunk := word
				if start > 0 && reply[start-1] == ' ' {
					chunk = " " + word
				}
				pos = start + len(word)
				uhhobjofollamaResp := ollamaResp{
					Model:     model,
					CreatedAt: createdAt,
					Message: msg{
						Role:    "assistant",
						Content: chunk,
					},
					Done: false,
				}
				respBytes, _ := json.Marshal(uhhobjofollamaResp)
				w.Write(respBytes)
				w.Write([]byte("\n"))
				flusher.Flush()
			}
			// spoofs final metadata that is present in ollama WHY idk but some services need it so...
			finalResp := ollamaResp{
				Model:              model,
				CreatedAt:          createdAt,
				Message:            msg{Role: "assistant", Content: ""},
				DoneReason:         "stop",
				Done:               true,
				TotalDuration:      4768114600, // Example values, replace with real timing if needed
				LoadDuration:       2497832600,
				PromptEvalCount:    84,
				PromptEvalDuration: 491959200,
				EvalCount:          37,
				EvalDuration:       1746310500,
			}
			respBytes, _ := json.Marshal(finalResp)
			w.Write(respBytes)
			w.Write([]byte("\n"))
			flusher.Flush()
			return
		}
		// sends a single json respsone incase of nonstream mode
		uhhobjofollamaResp := ollamaResp{
			Model:     model,
			CreatedAt: createdAt,
			Message: msg{
				Role:    "assistant",
				Content: reply,
			},
			DoneReason: "stop",
			Done:       true,
		}
		respBytes, _ := json.Marshal(uhhobjofollamaResp)
		w.Write(respBytes)
		w.Write([]byte("\n"))
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
			DoneReason: "stop",
			Done:       true,
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
			DoneReason: "stop",
			Done:       true,
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
			DoneReason: "stop",
			Done:       true,
		}
		respBytes, _ := json.Marshal(uhhobjofollamaResp)
		w.Write(respBytes)
		w.Write([]byte("\n"))
		flusher.Flush()
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// spoofs which models are available allowing services to see all your options.
func hTags(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{
	"models": [
		{
			"name": "gpt-4o",
			"model": "gpt-4o",
			"modified_at": "2069-01-01T00:00:00Z",
			"size": 69,
			"digest": "yesiputfunnynumberabove",
			"details": {
				"parent_model": "fuck you",
				"format": "openai",
				"family": "gpt-4o",
				"families": ["gpt-4o"],
				"parameter_size": "yes",
				"quantization_level": "i"
			}
		},
		{
			"name": "gpt-4o-mini",
			"model": "gpt-4o-mini",
			"modified_at": "2069-01-01T00:00:00Z",
			"size": 69,
			"digest": "yesiputfunnynumberabove",
			"details": {
				"parent_model": "don't",
				"format": "openai",
				"family": "gpt-4o-mini",
				"families": ["gpt-4o-mini"],
				"parameter_size": "know",
				"quantization_level": "what"
			}
		},
		{
			"name": "gpt-4.1-nano",
			"model": "gpt-4.1-nano",
			"modified_at": "2069-01-01T00:00:00Z",
			"size": 69,
			"digest": "yesiputfunnynumberabove",
			"details": {
				"parent_model": "to",
				"format": "openai",
				"family": "gpt-4.1-nano",
				"families": ["gpt-4.1-nano"],
				"parameter_size": "put",
				"quantization_level": "here"
			}
		},
		{
			"name": "gpt-4.1-mini",
			"model": "gpt-4.1-mini",
			"modified_at": "2069-01-01T00:00:00Z",
			"size": 69,
			"digest": "yesiputfunnynumberabove",
			"details": {
				"parent_model": "so",
				"format": "fuck",
				"family": "gpt-4.1-mini",
				"families": ["gpt-4.1-mini"],
				"parameter_size": "off",
				"quantization_level": ":)" 
			}
		},
		{
			"name": "gpt-4.1",
			"model": "gpt-4.1",
			"modified_at": "2069-01-01T00:00:00Z",
			"size": 69,
			"digest": "yesiputfunnynumberabove",
			"details": {
				"parent_model": "too",
				"format": "openai",
				"family": "gpt-4.1",
				"families": ["gpt-4.1"],
				"parameter_size": "many",
				"quantization_level": "models"
			}
		},
		{
			"name": "gpt-3.5",
			"model": "gpt-3.5",
			"modified_at": "2069-01-01T00:00:00Z",
			"size": 69,
			"digest": "yesiputfunnynumberabove",
			"details": {
				"parent_model": "i",
				"format": "openai",
				"family": "gpt-3.5",
				"families": ["gpt-3.5"],
				"parameter_size": "s",
				"quantization_level": "t"
			}
		},
		{
			"name": "tts",
			"model": "tts",
			"modified_at": "2069-01-01T00:00:00Z",
			"size": 69,
			"digest": "yesiputfunnynumberabove",
			"details": {
				"parent_model": "g",
				"format": "openai",
				"family": "tts",
				"families": ["tts"],
				"parameter_size": "x",
				"quantization_level": "d"
			}
		},
		{
			"name": "base64",
			"model": "base64",
			"modified_at": "2069-01-01T00:00:00Z",
			"size": 69,
			"digest": "yesiputfunnynumberabove",
			"details": {
				"parent_model": "does",
				"format": "openai (not really just have nothing to put here)",
				"family": "base64",
				"families": ["base64"],
				"parameter_size": "it",
				"quantization_level": "ever"
			}
		},
		{
			"name": "dall-e-3",
			"model": "dall-e-3",
			"modified_at": "2069-01-01T00:00:00Z",
			"size": 69,
			"digest": "yesiputfunnynumberabove",
			"details": {
				"parent_model": "stop",
				"format": "openai",
				"family": "dall-e-3",
				"families": ["dall-e-3"],
				"parameter_size": "finally",
				"quantization_level": "!!!"
			}
		}
	]
}`))
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
