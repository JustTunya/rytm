package queue

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)
func (m *Manager) transcodeWithMeta(ctx context.Context, rawPath, coverPath string, meta TrackMeta, outPath string, taskID string) error {
	hasCover := coverPath != ""
	var sb strings.Builder
	sb.WriteString(";FFMETADATA1\r\n")
	if meta.Title != "" {
		sb.WriteString("title=")
		sb.WriteString(escapeMetadataValue(meta.Title))
		sb.WriteString("\r\n")
	}
	if meta.Artist != "" {
		sb.WriteString("artist=")
		sb.WriteString(escapeMetadataValue(meta.Artist))
		sb.WriteString("\r\n")
	}
	if meta.Album != "" {
		sb.WriteString("album=")
		sb.WriteString(escapeMetadataValue(meta.Album))
		sb.WriteString("\r\n")
	}
	if meta.Date != "" {
		sb.WriteString("date=")
		sb.WriteString(escapeMetadataValue(meta.Date))
		sb.WriteString("\r\n")
	}
	if meta.TrackNum != "" {
		sb.WriteString("track=")
		sb.WriteString(escapeMetadataValue(meta.TrackNum))
		sb.WriteString("\r\n")
	}
	if meta.Genre != "" {
		sb.WriteString("genre=")
		sb.WriteString(escapeMetadataValue(meta.Genre))
		sb.WriteString("\r\n")
	}
	metaPath := filepath.Join(os.TempDir(), fmt.Sprintf("rytm_%s_metadata.txt", taskID))
	if err := os.WriteFile(metaPath, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("write metadata file: %v", err)
	}
	defer os.Remove(metaPath)
	args := []string{"-y", "-i", rawPath}
	if hasCover {
		args = append(args, "-i", coverPath)
	}
	args = append(args, "-i", metaPath)
	args = append(args, "-map", "0:a")
	if hasCover {
		args = append(args, "-map", "1:v")
	}
	metaIdx := 1
	if hasCover {
		metaIdx = 2
	}
	args = append(args, "-map_metadata", strconv.Itoa(metaIdx))
	args = append(args, "-c:a", "aac", "-b:a", "256k")
	if hasCover {
		args = append(args, "-c:v", "copy")
	}
	args = append(args, "-af", "volume=-10dB")
	if hasCover {
		args = append(args, "-disposition:v:0", "attached_pic")
	}
	args = append(args, outPath)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Dir = "./"
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
func escapeMetadataValue(s string) string {
	r := strings.NewReplacer(
		"\\", "\\\\",
		"=", "\\=",
		";", "\\;",
		"#", "\\#",
		"\n", "\\\n",
	)
	return r.Replace(s)
}