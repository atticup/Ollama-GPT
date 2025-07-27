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

// Global dementia mode override: nil = ask user, true = always enable, false = always disable (just don't touch if u don't know what you're doing)
var dementiaOverride *bool

// HTTP client (shared) just makes requests faster
var sharedHTTPClient = &http.Client{
	Timeout: 60 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
		ForceAttemptHTTP2:   true,
	},
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

// ollamaResp is the response format for ollama (chat only yes i made /api/generate actually work yippe)
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

// ollamaGenerateResp is the response format for ollama generate (api/generate)
type ollamaGenerateResp struct {
	Model              string `json:"model"`
	CreatedAt          string `json:"created_at"`
	Response           string `json:"response"`
	DoneReason         string `json:"done_reason,omitempty"`
	Done               bool   `json:"done"`
	TotalDuration      int64  `json:"total_duration,omitempty"`
	LoadDuration       int64  `json:"load_duration,omitempty"`
	PromptEvalCount    int    `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64  `json:"prompt_eval_duration,omitempty"`
	EvalCount          int    `json:"eval_count,omitempty"`
	EvalDuration       int64  `json:"eval_duration,omitempty"`
}

func preWarmConnection() {
	if debug {
		fmt.Println("[DEBUG] prewarming connection to pfuner.xyz (just makes messages a bit faster)")
	}
	helloReq := chatReq{
		Messages: []string{"hello world"},
	}
	reqBody, _ := json.Marshal(helloReq)
	resp, err := sharedHTTPClient.Post("https://pfuner.xyz/v1/chat/completions", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		if debug {
			fmt.Printf("[DEBUG] prewarmup failed (this is normal just ignore and continue) %v\n", err)
		}
		return
	}
	defer resp.Body.Close()

	if debug {
		fmt.Println("[DEBUG] prewarmup successful connection is ready have fun")
	}
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
	if dementiaOverride == nil {
		dementiaCh := make(chan string, 1)
		go func() {
			fmt.Print("Press 'p' to enable dementia mode (basically if you're using a service that is a chatbot enable this): ")
			var dementiaInput string
			fmt.Scanln(&dementiaInput)
			dementiaCh <- dementiaInput
		}()
		select {
		case dementiaInput := <-dementiaCh:
			if strings.ToLower(strings.TrimSpace(dementiaInput)) == "p" {
				b := true
				dementiaOverride = &b
				fmt.Println("dementia mode enabled long messages will be automatically trimmed")
			} else {
				b := false
				dementiaOverride = &b
				fmt.Println("dementia mode disabled")
			}
		case <-time.After(3 * time.Second):
			b := false
			dementiaOverride = &b
			fmt.Println("\nno input in 3s dementia mode disabled")
		}
	} else if *dementiaOverride {
		fmt.Println("dementia mode forced ON long messages will be trimmed")
	} else {
		fmt.Println("dementia mode forced OFF")
	}

	// Pre-warm the connection in the background
	go preWarmConnection()
	http.HandleFunc("/api/chat", hChat)
	http.HandleFunc("/api/generate", hChat)
	http.HandleFunc("/api/tags", hTags)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

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

	isGenerateRequest := r.URL.Path == "/api/generate"

	var req ollamaReq
	//same thing as chat except entirely different
	if isGenerateRequest {
		var generateReq struct {
			Model   string      `json:"model"`
			Prompt  string      `json:"prompt"`
			System  string      `json:"system,omitempty"`
			Stream  bool        `json:"stream,omitempty"`
			Options interface{} `json:"options,omitempty"`
		}

		if err := json.NewDecoder(r.Body).Decode(&generateReq); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		req.Model = generateReq.Model
		req.Stream = generateReq.Stream
		req.Options = generateReq.Options
		if generateReq.System != "" {
			req.Messages = append(req.Messages, msg{
				Role:    "system",
				Content: generateReq.System,
			})
		}
		req.Messages = append(req.Messages, msg{
			Role:    "user",
			Content: generateReq.Prompt,
		})
	} else {
		// added the system ability so u can declare a personallity or roleplay for the sick freaks of you out there
		var raw map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		b, _ := json.Marshal(raw)
		if err := json.Unmarshal(b, &req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if sys, ok := raw["system"]; ok {
			if sysStr, ok := sys.(string); ok && sysStr != "" {
				req.Messages = append([]msg{{Role: "system", Content: sysStr}}, req.Messages...)
			}
		}
	}
	model := req.Model
	baseModel := model
	if strings.HasSuffix(model, ":latest") {
		baseModel = strings.TrimSuffix(model, ":latest")
	}
	var endpoint string
	var reqBody []byte
	contentType := "application/json"
	isChatStream := false
	isV2 := false
	switch baseModel {
	case "gpt-4o", "gpt-4o-mini", "gpt-4.1-nano", "gpt-4.1-mini", "gpt-4.1":
		// detects and blocks any request to do unnecessary api intensive tasks such as suggesting next question/chat name you can disable if u want i recommend not to (causes alot of unnecessary issues with ratelimits)
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "### Task:") {
				if debug {
					fmt.Printf("[DEBUG] Blocked request (unnecessary api spam)\n")
				}
				w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
				w.WriteHeader(http.StatusOK)

				var respBytes []byte
				if isGenerateRequest {
					ollamaErrResp := ollamaGenerateResp{
						Model:      model,
						CreatedAt:  nowRFC(),
						Response:   "Request blocked due to unnecessary api spam  (trying to predict next messages/chatname)",
						DoneReason: "stop",
						Done:       true,
					}
					respBytes, _ = json.Marshal(ollamaErrResp)
				} else {
					ollamaErrResp := ollamaResp{
						Model:     model,
						CreatedAt: nowRFC(),
						Message: msg{
							Role:    "assistant",
							Content: "Request blocked due to unnecessary api spam (trying to predict next messages/chatname)",
						},
						DoneReason: "stop",
						Done:       true,
					}
					respBytes, _ = json.Marshal(ollamaErrResp)
				}
				w.Write(respBytes)
				w.Write([]byte("\n"))
				return
			}
		}

		totalLength := 0
		for _, m := range req.Messages {
			totalLength += len(m.Content)
		}

		if totalLength > 8000 {
			if dementiaOverride != nil && *dementiaOverride {
				if debug {
					fmt.Printf("[DEBUG] GPT prompt too long (%d chars) using dementia mode to trim it down\n", totalLength)
				}
				req.Messages = circumsizeM(req.Messages, 8000)
			} else {
				if debug {
					fmt.Printf("[DEBUG] GPT prompt too long (%d chars) blocking request (use dementia mode if u want the messages to just be trimmed down)\n", totalLength)
				}
				w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
				w.WriteHeader(http.StatusOK)

				var respBytes []byte
				if isGenerateRequest {
					ollamaErrResp := ollamaGenerateResp{
						Model:      model,
						CreatedAt:  nowRFC(),
						Response:   "prompt too long please keep it under 8000 characters (or simply enable dementia mode next time on runtime)",
						DoneReason: "stop",
						Done:       true,
					}
					respBytes, _ = json.Marshal(ollamaErrResp)
				} else {
					ollamaErrResp := ollamaResp{
						Model:     model,
						CreatedAt: nowRFC(),
						Message: msg{
							Role:    "assistant",
							Content: "prompt too long please keep it under 8000 characters (or simply enable dementia mode next time on runtime)",
						},
						DoneReason: "stop",
						Done:       true,
					}
					respBytes, _ = json.Marshal(ollamaErrResp)
				}
				w.Write(respBytes)
				w.Write([]byte("\n"))
				return
			}
		}

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
			"model":       baseModel,
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
		if strings.Contains(prompt, "### Task:") {
			if debug {
				fmt.Printf("[DEBUG] Blocked unnecessary api spam\n")
			}
			w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
			w.WriteHeader(http.StatusOK)

			var respBytes []byte
			if isGenerateRequest {
				ollamaErrResp := ollamaGenerateResp{
					Model:      model,
					CreatedAt:  nowRFC(),
					Response:   "Request blocked due to unnecessary api spam",
					DoneReason: "stop",
					Done:       true,
				}
				respBytes, _ = json.Marshal(ollamaErrResp)
			} else {
				ollamaErrResp := ollamaResp{
					Model:     model,
					CreatedAt: nowRFC(),
					Message: msg{
						Role:    "assistant",
						Content: "Request blocked due to unnessary api spam",
					},
					DoneReason: "stop",
					Done:       true,
				}
				respBytes, _ = json.Marshal(ollamaErrResp)
			}
			w.Write(respBytes)
			w.Write([]byte("\n"))
			return
		}
		if len(prompt) > 1000 {
			if debug {
				fmt.Printf("[DEBUG] DALL-E prompt too long (%d chars) blocking request\n", len(prompt))
			}
			w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
			w.WriteHeader(http.StatusOK)

			var respBytes []byte
			if isGenerateRequest {
				ollamaErrResp := ollamaGenerateResp{
					Model:      model,
					CreatedAt:  nowRFC(),
					Response:   "please keep the text under 1000 characters (btw using image generation in chat mode is not smart)",
					DoneReason: "stop",
					Done:       true,
				}
				respBytes, _ = json.Marshal(ollamaErrResp)
			} else {
				ollamaErrResp := ollamaResp{
					Model:     model,
					CreatedAt: nowRFC(),
					Message: msg{
						Role:    "assistant",
						Content: "please keep the text under 1000 characters (btw using image generation in chat mode is not smart)",
					},
					DoneReason: "stop",
					Done:       true,
				}
				respBytes, _ = json.Marshal(ollamaErrResp)
			}
			w.Write(respBytes)
			w.Write([]byte("\n"))
			return
		}

		imgReq := map[string]interface{}{
			"model":  baseModel,
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

		if strings.Contains(prompt, "### Task:") {
			if debug {
				fmt.Printf("[DEBUG] Blocked unnecessary api spam\n")
			}
			w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
			w.WriteHeader(http.StatusOK)

			var respBytes []byte
			if isGenerateRequest {
				ollamaErrResp := ollamaGenerateResp{
					Model:      model,
					CreatedAt:  nowRFC(),
					Response:   "Request blocked due to unnecessary api spam",
					DoneReason: "stop",
					Done:       true,
				}
				respBytes, _ = json.Marshal(ollamaErrResp)
			} else {
				ollamaErrResp := ollamaResp{
					Model:     model,
					CreatedAt: nowRFC(),
					Message: msg{
						Role:    "assistant",
						Content: "Request blocked due to unnessary api spam",
					},
					DoneReason: "stop",
					Done:       true,
				}
				respBytes, _ = json.Marshal(ollamaErrResp)
			}
			w.Write(respBytes)
			w.Write([]byte("\n"))
			return
		}
		if len(prompt) > 1000 {
			if debug {
				fmt.Printf("[DEBUG] Base64 prompt too long (%d chars) blocking request\n", len(prompt))
			}
			w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
			w.WriteHeader(http.StatusOK)

			var respBytes []byte
			if isGenerateRequest {
				ollamaErrResp := ollamaGenerateResp{
					Model:      model,
					CreatedAt:  nowRFC(),
					Response:   "please keep the text under 1000 characters (btw using image generation in chat mode is not smart)",
					DoneReason: "stop",
					Done:       true,
				}
				respBytes, _ = json.Marshal(ollamaErrResp)
			} else {
				ollamaErrResp := ollamaResp{
					Model:     model,
					CreatedAt: nowRFC(),
					Message: msg{
						Role:    "assistant",
						Content: "please keep the text under 1000 characters (btw using image generation in chat mode is not smart)",
					},
					DoneReason: "stop",
					Done:       true,
				}
				respBytes, _ = json.Marshal(ollamaErrResp)
			}
			w.Write(respBytes)
			w.Write([]byte("\n"))
			return
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

		if strings.Contains(text, "### Task:") {
			if debug {
				fmt.Printf("[DEBUG] Blocked unnecessary api spam\n")
			}
			w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
			w.WriteHeader(http.StatusOK)

			var respBytes []byte
			if isGenerateRequest {
				ollamaErrResp := ollamaGenerateResp{
					Model:      model,
					CreatedAt:  nowRFC(),
					Response:   "Request blocked due to unnecessary api spam",
					DoneReason: "stop",
					Done:       true,
				}
				respBytes, _ = json.Marshal(ollamaErrResp)
			} else {
				ollamaErrResp := ollamaResp{
					Model:     model,
					CreatedAt: nowRFC(),
					Message: msg{
						Role:    "assistant",
						Content: "Request blocked due to unnessary api spam",
					},
					DoneReason: "stop",
					Done:       true,
				}
				respBytes, _ = json.Marshal(ollamaErrResp)
			}
			w.Write(respBytes)
			w.Write([]byte("\n"))
			return
		}
		if len(text) > 500 {
			if debug {
				fmt.Printf("[DEBUG] TTS text too long (%d chars) blocking request\n", len(text))
			}
			w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
			w.WriteHeader(http.StatusOK)

			var respBytes []byte
			if isGenerateRequest {
				ollamaErrResp := ollamaGenerateResp{
					Model:      model,
					CreatedAt:  nowRFC(),
					Response:   "please keep the text under 500 characters (btw using tts in chat is not smart)",
					DoneReason: "stop",
					Done:       true,
				}
				respBytes, _ = json.Marshal(ollamaErrResp)
			} else {
				ollamaErrResp := ollamaResp{
					Model:     model,
					CreatedAt: nowRFC(),
					Message: msg{
						Role:    "assistant",
						Content: "please keep the text under 500 characters (btw using tts in chat is not smart)",
					},
					DoneReason: "stop",
					Done:       true,
				}
				respBytes, _ = json.Marshal(ollamaErrResp)
			}
			w.Write(respBytes)
			w.Write([]byte("\n"))
			return
		}

		ttsReq := map[string]interface{}{
			"text": text,
		}
		reqBody, _ = json.Marshal(ttsReq)
	default:
		if debug {
			fmt.Printf("[DEBUG] Model '%s' not matched, falling back to v1 endpoint\n", baseModel)
		}

		// detects and blocks any request to do unnecessary api intensive tasks such as suggesting next question/chat name you can disable if u want i recommend not to (causes alot of unnecessary issues with ratelimits)
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "### Task:") {
				if debug {
					fmt.Printf("[DEBUG] Blocked request (unnecessary api spam)\n")
				}
				w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
				w.WriteHeader(http.StatusOK)

				var respBytes []byte
				if isGenerateRequest {
					ollamaErrResp := ollamaGenerateResp{
						Model:      model,
						CreatedAt:  nowRFC(),
						Response:   "Request blocked due to unnecessary api spam (trying to predict next messages/chatname)",
						DoneReason: "stop",
						Done:       true,
					}
					respBytes, _ = json.Marshal(ollamaErrResp)
				} else {
					ollamaErrResp := ollamaResp{
						Model:     model,
						CreatedAt: nowRFC(),
						Message: msg{
							Role:    "assistant",
							Content: "Request blocked due to unnecessary api spam (trying to predict next messages/chatname)",
						},
						DoneReason: "stop",
						Done:       true,
					}
					respBytes, _ = json.Marshal(ollamaErrResp)
				}
				w.Write(respBytes)
				w.Write([]byte("\n"))
				return
			}
		}

		totalLength := 0
		for _, m := range req.Messages {
			totalLength += len(m.Content)
		}

		if totalLength > 2000 {
			if dementiaOverride != nil && *dementiaOverride {
				if debug {
					fmt.Printf("[DEBUG] Default model prompt too long (%d chars) using dementia mode to trim it down\n", totalLength)
				}
				req.Messages = circumsizeM(req.Messages, 2000)
			} else {
				if debug {
					fmt.Printf("[DEBUG] Default model prompt too long (%d chars) blocking request (use dementia mode if u want the messages to just be trimmed down)\n", totalLength)
				}
				w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
				w.WriteHeader(http.StatusOK)

				var respBytes []byte
				if isGenerateRequest {
					ollamaErrResp := ollamaGenerateResp{
						Model:      model,
						CreatedAt:  nowRFC(),
						Response:   "prompt too long please keep it under 2000 characters (or simply enable dementia mode next time on runtime)",
						DoneReason: "stop",
						Done:       true,
					}
					respBytes, _ = json.Marshal(ollamaErrResp)
				} else {
					ollamaErrResp := ollamaResp{
						Model:     model,
						CreatedAt: nowRFC(),
						Message: msg{
							Role:    "assistant",
							Content: "prompt too long please keep it under 2000 characters (or simply enable dementia mode next time on runtime)",
						},
						DoneReason: "stop",
						Done:       true,
					}
					respBytes, _ = json.Marshal(ollamaErrResp)
				}
				w.Write(respBytes)
				w.Write([]byte("\n"))
				return
			}
		}

		endpoint = "https://pfuner.xyz/v1/chat/completions"
		var messages []string
		for _, m := range req.Messages {
			messages = append(messages, m.Content)
		}
		chatReq := chatReq{
			Messages: messages,
		}
		fmt.Printf("[DEBUG] Sending message", messages)
		reqBody, _ = json.Marshal(chatReq)
		isChatStream = true
	}
	if debug {
		fmt.Printf("[DEBUG] Sending request to %s\n", endpoint)
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

	// Check if response is HTML (likely blocked by Cloudflare or other protection)
	if strings.HasPrefix(string(body), `{"reply":"<!DOCTYPE html>\`) || strings.HasPrefix(string(body), "<html>") {
		if debug {
			fmt.Printf("[DEBUG] HTML response detected, likely blocked by Cloudflare\n")
		}
		w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
		w.WriteHeader(http.StatusOK)

		var respBytes []byte
		if isGenerateRequest {
			ollamaErrResp := ollamaGenerateResp{
				Model:      model,
				CreatedAt:  nowRFC(),
				Response:   "Response was blocked please try again in a minute...",
				DoneReason: "stop",
				Done:       true,
			}
			respBytes, _ = json.Marshal(ollamaErrResp)
		} else {
			ollamaErrResp := ollamaResp{
				Model:     model,
				CreatedAt: nowRFC(),
				Message: msg{
					Role:    "assistant",
					Content: "Response was blocked please try again in a minute...",
				},
				DoneReason: "stop",
				Done:       true,
			}
			respBytes, _ = json.Marshal(ollamaErrResp)
		}
		w.Write(respBytes)
		w.Write([]byte("\n"))
		return
	}

	//added support for x-ndjson + fixed some problems with the /api/generate ratelimit errors
	if resp.StatusCode == 429 || strings.Contains(string(body), "\"Too many requests (\"") {
		w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
		w.WriteHeader(http.StatusOK)

		var respBytes []byte
		if isGenerateRequest {
			ollamaErrResp := ollamaGenerateResp{
				Model:      model,
				CreatedAt:  nowRFC(),
				Response:   "Too many requests please wait a min... (contact atticus if you think higher request limits should be set)",
				DoneReason: "stop",
				Done:       true,
			}
			respBytes, _ = json.Marshal(ollamaErrResp)
		} else {
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
			respBytes, _ = json.Marshal(ollamaErrResp)
		}
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
		} else {
			// fixed issues in some services by setting stream to on unless said otherwise by the service in ask mode
			stream = true
		}
		if stream {
			// actually proper x-ndjson (and no i don't have an idea on why half of this is a requirement but without it shit just turned into base64😭)
			w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
			w.Header().Set("Connection", "keep-alive")
			w.Header().Set("Transfer-Encoding", "chunked")
			w.Header().Set("X-Accel-Buffering", "no")
			w.Header().Set("Access-Control-Expose-Headers", "Content-Type")
			w.WriteHeader(http.StatusOK)
			// Remove all U+000A (Line Feed) characters from reply
			reply = strings.ReplaceAll(reply, "\n", "")
			cleaned := make([]rune, 0, len(reply))
			for _, r := range reply {
				// changed a bit to support new x-ndjson working properly
				if (r >= 0x20 && r <= 0x7E) || r == 0x09 || (r >= 0x80) {
					cleaned = append(cleaned, r)
				}
			}
			reply = string(cleaned)
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "unsupported stream...", http.StatusInternalServerError)
				return
			}
			// Stream shit in chunks to be faster and require less jsons (probably foreshadowing but might cause some problems in future)
			chunkSize := 10
			for i := 0; i < len(reply); i += chunkSize {
				end := i + chunkSize
				if end > len(reply) {
					end = len(reply)
				}
				chunk := reply[i:end]

				var respBytes []byte
				if isGenerateRequest {
					generateResp := ollamaGenerateResp{
						Model:     model,
						CreatedAt: createdAt,
						Response:  chunk,
						Done:      false,
					}
					respBytes, _ = json.Marshal(generateResp)
				} else {
					chatResp := ollamaResp{
						Model:     model,
						CreatedAt: createdAt,
						Message: msg{
							Role:    "assistant",
							Content: chunk,
						},
						Done: false,
					}
					respBytes, _ = json.Marshal(chatResp)
				}

				// Ensure proper JSON line separation with explicit newline
				w.Write(respBytes)
				w.Write([]byte("\n"))
				flusher.Flush()
				time.Sleep(10 * time.Millisecond) //yes it's pretty much required for some web services which are slow in the brain
			}
			// spoofs final metadata that is present in ollama WHY idk but some services need it so...
			var finalrespbytes []byte
			//modified a bit to work with /api/generate
			if isGenerateRequest {
				finalResp := ollamaGenerateResp{
					Model:              model,
					CreatedAt:          createdAt,
					Response:           "",
					DoneReason:         "stop",
					Done:               true,
					TotalDuration:      4768114600, // Example values, replace with real timing if needed (probably not required)
					LoadDuration:       2497832600,
					PromptEvalCount:    84,
					PromptEvalDuration: 491959200,
					EvalCount:          37,
					EvalDuration:       1746310500,
				}
				finalrespbytes, _ = json.Marshal(finalResp)
			} else {
				finalResp := ollamaResp{
					Model:              model,
					CreatedAt:          createdAt,
					Message:            msg{Role: "assistant", Content: ""},
					DoneReason:         "stop",
					Done:               true,
					TotalDuration:      4768114600, // Example values, replace with real timing if needed (probably not required)
					LoadDuration:       2497832600,
					PromptEvalCount:    84,
					PromptEvalDuration: 491959200,
					EvalCount:          37,
					EvalDuration:       1746310500,
				}
				finalrespbytes, _ = json.Marshal(finalResp)
			}
			w.Write(finalrespbytes)
			w.Write([]byte("\n"))
			flusher.Flush()
			return
		}
		// single json for nostream /api/generate
		var respBytes []byte
		if isGenerateRequest {
			generateResp := ollamaGenerateResp{
				Model:      model,
				CreatedAt:  createdAt,
				Response:   reply,
				DoneReason: "stop",
				Done:       true,
			}
			respBytes, _ = json.Marshal(generateResp)
		} else {
			chatResp := ollamaResp{
				Model:     model,
				CreatedAt: createdAt,
				Message: msg{
					Role:    "assistant",
					Content: reply,
				},
				DoneReason: "stop",
				Done:       true,
			}
			respBytes, _ = json.Marshal(chatResp)
		}
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
		w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
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
		var respBytes []byte
		if isGenerateRequest {
			generateResp := ollamaGenerateResp{
				Model:      model,
				CreatedAt:  createdAt,
				Response:   imageURL,
				DoneReason: "stop",
				Done:       true,
			}
			respBytes, _ = json.Marshal(generateResp)
		} else {
			chatResp := ollamaResp{
				Model:     model,
				CreatedAt: createdAt,
				Message: msg{
					Role:    "assistant",
					Content: imageURL,
				},
				DoneReason: "stop",
				Done:       true,
			}
			respBytes, _ = json.Marshal(chatResp)
		}
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
		w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
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
		var respBytes []byte
		if isGenerateRequest {
			generateResp := ollamaGenerateResp{
				Model:      model,
				CreatedAt:  createdAt,
				Response:   base64str,
				DoneReason: "stop",
				Done:       true,
			}
			respBytes, _ = json.Marshal(generateResp)
		} else {
			chatResp := ollamaResp{
				Model:     model,
				CreatedAt: createdAt,
				Message: msg{
					Role:    "assistant",
					Content: base64str,
				},
				DoneReason: "stop",
				Done:       true,
			}
			respBytes, _ = json.Marshal(chatResp)
		}
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
		w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "unsupported stream...", http.StatusInternalServerError)
			return
		}
		var respBytes []byte
		if isGenerateRequest {
			generateResp := ollamaGenerateResp{
				Model:      model,
				CreatedAt:  createdAt,
				Response:   ttsResp.URL,
				DoneReason: "stop",
				Done:       true,
			}
			respBytes, _ = json.Marshal(generateResp)
		} else {
			chatResp := ollamaResp{
				Model:     model,
				CreatedAt: createdAt,
				Message: msg{
					Role:    "assistant",
					Content: ttsResp.URL,
				},
				DoneReason: "stop",
				Done:       true,
			}
			respBytes, _ = json.Marshal(chatResp)
		}
		w.Write(respBytes)
		w.Write([]byte("\n"))
		flusher.Flush()
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// spoofs which models are available allowing services to see all your options.
func hTags(w http.ResponseWriter, r *http.Request) {
	// Add CORS headers for tags endpoint
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	//changed everything to add :latest since doesn't work without it 🫠
	w.Write([]byte(`{ 
	"models": [
		{
			"name": "gpt-4o:latest",
			"model": "gpt-4o:latest",
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
			"name": "gpt-4o-mini:latest",
			"model": "gpt-4o-mini:latest",
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
			"name": "gpt-4.1-nano:latest",
			"model": "gpt-4.1-nano:latest",
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
			"name": "gpt-4.1-mini:latest",
			"model": "gpt-4.1-mini:latest",
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
			"name": "gpt-4.1:latest",
			"model": "gpt-4.1:latest",
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
			"name": "gpt-3.5:latest",
			"model": "gpt-3.5:latest",
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
			"name": "tts:latest",
			"model": "tts:latest",
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
			"name": "base64:latest",
			"model": "base64:latest",
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
			"name": "dall-e-3:latest",
			"model": "dall-e-3:latest",
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

// basically just trims the tip of the message down if it's too long xd (apart of dementia mode)
func circumsizeM(messages []msg, maxLength int) []msg {
	if len(messages) == 0 {
		return messages
	}
	totalLength := 0
	for _, m := range messages {
		totalLength += len(m.Content)
	}
	if totalLength <= maxLength {
		return messages
	}
	circumsized := make([]msg, 0, len(messages))
	systemMessages := make([]msg, 0)
	for _, m := range messages {
		if m.Role == "system" {
			systemMessages = append(systemMessages, m)
		}
	}

	currentLength := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "system" {
			continue // Skip important instructions cuz u don't want it being clueless on how to behave
		}

		if currentLength+len(messages[i].Content) <= maxLength {
			circumsized = append([]msg{messages[i]}, circumsized...)
			currentLength += len(messages[i].Content)
		} else {
			break
		}
	}

	result := append(systemMessages, circumsized...)
	if debug {
		fmt.Printf("[DEBUG] Prompt circumsized from %d to %d characters\n", totalLength, currentLength)
	}

	return result
}

func nowRFC() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.0000000Z")
}
