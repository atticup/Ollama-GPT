# Ollama-GPT

This is a Go application that acts as a drop-in replacement for Ollama, redirecting requests to various endpoints on `pfuner.xyz` (a free service created by me that allows 24/7 api access to chatgpt api) depending on the model type. It supports chat, image generation, base64 image output, and text-to-speech (TTS), and streams responses back just like ollama (basically any program or project that uses ollama's api http://127.0.0.1:11434 is now gonna be able to also run chatgpt api for completely free) THIS IS A REPLACEMENT NOT AN ADDITION TO OLLAMA U WILL NEED TO CLOSE IT 

## Features

- **Ollama compatible API**: Accepts requests in the same format as Ollama
- **Multiple model support**: Handles chat, image generation, base64, and TTS models
- **Streaming responses**: Streams chat responses in chunks for compatibility with previous ollama apps
- **Response transformation**: Converts all responses back to Ollama format (meaning any apps developed for ollama will also work now with the chatgpt api completely for free)
- **Completely free & annonymous**: The project is completely free and annoymous pfuner acts as a proxy to annonymize and prevent requests being tracked back to you.
- notice: while the project is completely free i would appreciate a star on it if you enjoyed as always have fun!

## Usage

### Running the server

```bash
go run OllamaGPT.go
```
**or**

run the executable provided in releases

The server will start on `http://127.0.0.1:11434` (the default Ollama port So you will need to close ollama before hand).

### Making requests

Send POST requests to `http://127.0.0.1:11434/api/chat` with the following format:

```json
{
  "model": "gpt-4o", // or "dall-e-3", "base64", "tts", etc... (anything invalid will default to gpt-3.5)
  "messages": [
    { "role": "system", "content": "You are a helpful assistant" },
    { "role": "user", "content": "Hello, how are you?" }
  ]
}
```

### Supported models and endpoints

- `gpt-4o`, `gpt-4o-mini`, `gpt-4.1-nano`, `gpt-4.1-mini`, `gpt-4.1`: Chat (proxied to `pfuner.xyz/v2/chat/completions`)
- `dall-e-3`: Image generation (`pfuner.xyz/v3/images/generations`)
- `base64`: Base64 image output (`pfuner.xyz/v4/images/generations`)
- `tts`: Text-to-speech (`pfuner.xyz/v5/audio/generations`)
- Any other will be directed to default gpt-3.5 model (`pfuner.xyz/v1/chat/completions`)

### Response format

The server returns responses in Ollama format:

```json
{
  "model": "any-model-name",
  "created_at": "2025-01-01T00:00:00Z",
  "message": {
    "role": "assistant",
    "content": "Hello! I'm doing well, thank you for asking."
  },
  "done": true
}
```

- For chat models responses are streamed in chunks.
- For image models the `content` field contains the image url or base64 string.
- For TTS the `content` field contains the audio url.

### Rate limiting and error handling

If the upstream API returns HTTP 429 or an error message containing "Too many requests", the server responds with:
To view ratelimits please visit `pfuner.xyz` in addition u can make a issue request on github to increase ratelimits if needed

```json
{
  "model": "...",
  "created_at": "...",
  "message": {
    "role": "assistant",
    "content": "Too many requests please wait a min... (contact atticus if you think higher request limits should be set)"
  },
  "done": true
}
```

## How it works

1. Accepts POST requests to `/api/chat`
2. Parses the Ollama request format
3. Determines the correct `pfuner.xyz` endpoint based on the model
4. Forwards the request, transforming the payload as needed
5. Converts the response back to Ollama format (streaming for chat)
6. Handles rate limits and errors gracefully
7. Returns the response to the client

## Building

To build the executable:

```bash
go build -o ollama-gpt.exe OllamaGPT.go
```

**To build with no console window (Windows GUI mode):**

```bash
go build -ldflags -H=windowsgui -o ollama-gpt.exe OllamaGPT.go
```

This will run without opening a console window (useful if you want it to run silently in the background on windows)

Then run:

```bash
.\ollama-gpt.exe
```

btw i used ai to write the readme cuz there ain't NO WAY my adhd brain is gonna be able to handle writing all of this manually
as always made with ❤️ by atticus
