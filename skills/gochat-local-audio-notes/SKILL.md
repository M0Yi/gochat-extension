---
name: gochat-local-audio-notes
description: "Handle GoChat audio attachments locally for transcription, meeting notes, action items, and concise call summaries. Use when a GoChat conversation includes an audio file or the user asks to transcribe, summarize, or extract minutes from a recording."
---

# GoChat Local Audio Notes

Use this skill when a GoChat message includes an audio attachment or the user asks for transcription, meeting notes, call summary, action items, or speaker highlights from a recording.

## Local Transcription Script

Use the bundled script first when the runtime exposes the audio file on disk:

```bash
python3 ~/.openclaw/skills/gochat-local-audio-notes/scripts/transcribe_audio.py /path/to/audio.m4a
```

Useful engine options:

- `--engine auto`: choose the best installed backend automatically
- `--engine whisper`: default local backend, usually the easiest to get working
- `--engine faster-whisper`: faster and usually better for longer recordings
- `--engine mlx-whisper`: strong option on Apple Silicon when installed
- `--engine whisper-cpp`: use a local `whisper-cli` binary if available

Useful model options:

- `--model base`: fast/light
- `--model small`: better quality
- `--model medium`: heavier but better for meetings
- `--model large-v3`: strongest quality, slowest and most memory hungry

If the user wants a heavier engine and it is not installed yet, use:

```bash
bash ~/.openclaw/skills/gochat-local-audio-notes/scripts/install_audio_backend.sh faster-whisper
```

On Apple Silicon, also consider:

```bash
bash ~/.openclaw/skills/gochat-local-audio-notes/scripts/install_audio_backend.sh mlx-whisper
```

## Goal

Prefer the audio file already attached to the OpenClaw message context. Work locally from the attachment passed into the conversation instead of depending on server-side STT for the final result.

## Workflow

1. Inspect the current message context for audio attachments first.
2. If multiple audio files are attached, list them briefly and choose the most likely primary recording by duration or filename.
3. If the user did not specify an output format, default to:
   - a short summary
   - key discussion points
   - action items with owners when identifiable
   - open questions / risks
4. If the user explicitly asks for a full transcript, provide the transcript first, then the summary.
5. If the user asks for meeting minutes, prefer a structured output:
   - Meeting summary
   - Decisions
   - Action items
   - Follow-ups

## Audio-first Guidance

- Treat attached audio as the source of truth even if the chat already contains a partial transcript.
- Prefer processing the local attachment directly when the model/runtime can access audio attachments from context.
- For long recordings, prefer `faster-whisper` or `mlx-whisper` with `small`, `medium`, or `large-v3`.
- If attachment metadata or filenames suggest chunks, summarize each chunk and then provide a merged overall summary.
- Preserve uncertainty. If a phrase is unclear, mark it as uncertain instead of inventing content.

## Output Defaults

### Default summary

- 3-8 bullet points for the main discussion
- an `Action items` section
- an `Open questions` section when relevant

### Full transcript

- Use timestamped blocks when timestamps are available from the runtime
- Keep obvious filler words only when they affect meaning
- Mark inaudible segments as `[inaudible]`

### Meeting minutes

- Title: infer from context if possible
- Attendees: only include if explicitly identifiable
- Decisions: only include confirmed decisions
- Action items: use `owner - task - due date` when available

## Guardrails

- Do not claim a complete transcript if the audio attachment was not actually available to the runtime.
- If the audio attachment is missing or unreadable, say that plainly and ask for a supported audio file.
- If the attachment is very long, offer a staged result:
  - transcript chunking
  - summary first
  - action items only
- Default language should match the user's message. Use Chinese when the conversation is in Chinese.
- When selecting an engine, tell the user if you are using a heavier backend because it may be slower but more accurate.
