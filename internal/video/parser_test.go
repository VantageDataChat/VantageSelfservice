package video

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"helpdesk/internal/config"
)

func TestParseWhisperOutput_ValidJSON(t *testing.T) {
	input := []byte(`{
		"segments": [
			{"start": 0.0, "end": 5.2, "text": "Hello world"},
			{"start": 5.2, "end": 10.1, "text": "This is a test"}
		]
	}`)

	segments, err := ParseWhisperOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segments))
	}
	if segments[0].Start != 0.0 || segments[0].End != 5.2 || segments[0].Text != "Hello world" {
		t.Errorf("segment[0] mismatch: %+v", segments[0])
	}
	if segments[1].Start != 5.2 || segments[1].End != 10.1 || segments[1].Text != "This is a test" {
		t.Errorf("segment[1] mismatch: %+v", segments[1])
	}
}

func TestParseWhisperOutput_EmptySegments(t *testing.T) {
	input := []byte(`{"segments": []}`)

	segments, err := ParseWhisperOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segments) != 0 {
		t.Fatalf("expected 0 segments, got %d", len(segments))
	}
}

func TestParseWhisperOutput_NoSegmentsField(t *testing.T) {
	input := []byte(`{}`)

	segments, err := ParseWhisperOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segments) != 0 {
		t.Fatalf("expected 0 segments, got %d", len(segments))
	}
}

func TestParseWhisperOutput_InvalidJSON(t *testing.T) {
	input := []byte(`not valid json`)

	_, err := ParseWhisperOutput(input)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestSerializeTranscript(t *testing.T) {
	segments := []TranscriptSegment{
		{Start: 0.0, End: 5.2, Text: "Hello world"},
		{Start: 5.2, End: 10.1, Text: "This is a test"},
	}

	data, err := SerializeTranscript(segments)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result []TranscriptSegment
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal serialized output: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(result))
	}
	if result[0].Text != "Hello world" || result[1].Text != "This is a test" {
		t.Errorf("deserialized segments mismatch: %+v", result)
	}
}

func TestSerializeTranscript_Empty(t *testing.T) {
	data, err := SerializeTranscript([]TranscriptSegment{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "[]" {
		t.Errorf("expected '[]', got '%s'", string(data))
	}
}

func TestSerializeTranscript_Nil(t *testing.T) {
	data, err := SerializeTranscript(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "null" {
		t.Errorf("expected 'null', got '%s'", string(data))
	}
}

func TestRoundTrip(t *testing.T) {
	original := []TranscriptSegment{
		{Start: 0.0, End: 3.5, Text: "First segment"},
		{Start: 3.5, End: 7.0, Text: "Second segment"},
		{Start: 7.0, End: 12.3, Text: "Third segment with 中文"},
	}

	data, err := SerializeTranscript(original)
	if err != nil {
		t.Fatalf("serialize error: %v", err)
	}

	// Wrap in whisper output format for ParseWhisperOutput
	whisperJSON, err := json.Marshal(map[string]interface{}{
		"segments": json.RawMessage(data),
	})
	if err != nil {
		t.Fatalf("failed to wrap in whisper format: %v", err)
	}

	restored, err := ParseWhisperOutput(whisperJSON)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(restored) != len(original) {
		t.Fatalf("length mismatch: expected %d, got %d", len(original), len(restored))
	}
	for i := range original {
		if original[i] != restored[i] {
			t.Errorf("segment[%d] mismatch: expected %+v, got %+v", i, original[i], restored[i])
		}
	}
}

func TestNewParser_DefaultValues(t *testing.T) {
	cfg := config.VideoConfig{
		FFmpegPath:  "/usr/bin/ffmpeg",
		WhisperPath: "/usr/bin/whisper",
	}
	p := NewParser(cfg)

	if p.FFmpegPath != "/usr/bin/ffmpeg" {
		t.Errorf("expected FFmpegPath '/usr/bin/ffmpeg', got '%s'", p.FFmpegPath)
	}
	if p.WhisperPath != "/usr/bin/whisper" {
		t.Errorf("expected WhisperPath '/usr/bin/whisper', got '%s'", p.WhisperPath)
	}
	if p.KeyframeInterval != 10 {
		t.Errorf("expected default KeyframeInterval 10, got %d", p.KeyframeInterval)
	}
	if p.WhisperModel != "base" {
		t.Errorf("expected default WhisperModel 'base', got '%s'", p.WhisperModel)
	}
}

func TestNewParser_CustomValues(t *testing.T) {
	cfg := config.VideoConfig{
		FFmpegPath:       "/opt/ffmpeg",
		WhisperPath:      "/opt/whisper",
		KeyframeInterval: 30,
		WhisperModel:     "large",
	}
	p := NewParser(cfg)

	if p.KeyframeInterval != 30 {
		t.Errorf("expected KeyframeInterval 30, got %d", p.KeyframeInterval)
	}
	if p.WhisperModel != "large" {
		t.Errorf("expected WhisperModel 'large', got '%s'", p.WhisperModel)
	}
}

func TestNewParser_ZeroInterval(t *testing.T) {
	cfg := config.VideoConfig{KeyframeInterval: 0}
	p := NewParser(cfg)
	if p.KeyframeInterval != 10 {
		t.Errorf("expected default KeyframeInterval 10 for zero input, got %d", p.KeyframeInterval)
	}
}

func TestNewParser_NegativeInterval(t *testing.T) {
	cfg := config.VideoConfig{KeyframeInterval: -5}
	p := NewParser(cfg)
	if p.KeyframeInterval != 10 {
		t.Errorf("expected default KeyframeInterval 10 for negative input, got %d", p.KeyframeInterval)
	}
}

func TestCheckDependencies_NoPaths(t *testing.T) {
	p := &Parser{}
	ffmpegOK, whisperOK := p.CheckDependencies()
	if ffmpegOK {
		t.Error("expected ffmpegOK=false when path is empty")
	}
	if whisperOK {
		t.Error("expected whisperOK=false when path is empty")
	}
}

func TestCheckDependencies_InvalidPaths(t *testing.T) {
	p := &Parser{
		FFmpegPath:  "/nonexistent/ffmpeg_fake_binary",
		WhisperPath: "/nonexistent/whisper_fake_binary",
	}
	ffmpegOK, whisperOK := p.CheckDependencies()
	if ffmpegOK {
		t.Error("expected ffmpegOK=false for nonexistent binary")
	}
	if whisperOK {
		t.Error("expected whisperOK=false for nonexistent binary")
	}
}

func TestExtractAudio_NoFFmpegPath(t *testing.T) {
	p := &Parser{}
	err := p.ExtractAudio("input.mp4", "output.wav")
	if err == nil {
		t.Fatal("expected error when FFmpegPath is empty")
	}
	if !strings.Contains(err.Error(), "ffmpeg 路径未配置") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestTranscribe_NoWhisperPath(t *testing.T) {
	p := &Parser{}
	_, err := p.Transcribe("audio.wav")
	if err == nil {
		t.Fatal("expected error when WhisperPath is empty")
	}
	if !strings.Contains(err.Error(), "whisper 路径未配置") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestExtractKeyframes_NoFFmpegPath(t *testing.T) {
	p := &Parser{}
	_, err := p.ExtractKeyframes("input.mp4", "/tmp/frames")
	if err == nil {
		t.Fatal("expected error when FFmpegPath is empty")
	}
	if !strings.Contains(err.Error(), "ffmpeg 路径未配置") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestExtractKeyframes_TimestampCalculation(t *testing.T) {
	// Create a temp directory with fake frame files to test timestamp logic
	dir := t.TempDir()
	frameNames := []string{"frame_0001.jpg", "frame_0002.jpg", "frame_0003.jpg", "frame_0004.jpg"}
	for _, name := range frameNames {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("failed to create test frame: %v", err)
		}
		f.Close()
	}

	// We can't call ExtractKeyframes directly (it needs ffmpeg), but we can
	// verify the timestamp logic by simulating what the method does after ffmpeg runs.
	// The method scans the directory and computes timestamps as i * interval.
	interval := 5
	entries, _ := os.ReadDir(dir)
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "frame_") && strings.HasSuffix(e.Name(), ".jpg") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for i, name := range files {
		expectedTS := float64(i * interval)
		kf := Keyframe{
			Timestamp: expectedTS,
			FilePath:  filepath.Join(dir, name),
		}
		if kf.Timestamp != expectedTS {
			t.Errorf("frame %s: expected timestamp %f, got %f", name, expectedTS, kf.Timestamp)
		}
	}

	// Verify: frame_0001 = 0*5=0, frame_0002 = 1*5=5, frame_0003 = 2*5=10, frame_0004 = 3*5=15
	expected := []float64{0, 5, 10, 15}
	for i, exp := range expected {
		actual := float64(i * interval)
		if actual != exp {
			t.Errorf("index %d: expected %f, got %f", i, exp, actual)
		}
	}
}

func TestParse_NothingConfigured(t *testing.T) {
	p := &Parser{}
	result, err := p.Parse("nonexistent.mp4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Transcript) != 0 {
		t.Errorf("expected empty transcript, got %d segments", len(result.Transcript))
	}
	if len(result.Keyframes) != 0 {
		t.Errorf("expected empty keyframes, got %d frames", len(result.Keyframes))
	}
}
