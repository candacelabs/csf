package csf

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
)

var simulationArtifactName = regexp.MustCompile(`^(manifest\.json|frames\.json|trace\.jsonl|events\.jsonl|progress-errors\.jsonl|upload-receipt\.json|camera-step-[0-9]{6}\.png)$`)

func (local *LocalSimulations) artifactViews(runID string, simulator pb.Simulator) []*pb.SimulationArtifact {
	profile := local.profile(simulator)
	if profile == nil || !simulationID.MatchString(runID) {
		return nil
	}
	root, err := os.OpenRoot(filepath.Join(profile.ArtifactDirectory, runID))
	if err != nil {
		return nil
	}
	defer func() { _ = root.Close() }()
	directory, err := root.Open(".")
	if err != nil {
		return nil
	}
	defer func() { _ = directory.Close() }()
	entries, err := directory.ReadDir(512)
	if err != nil && err != io.EOF {
		return nil
	}
	result := make([]*pb.SimulationArtifact, 0, len(entries))
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !simulationArtifactName.MatchString(entry.Name()) {
			continue
		}
		file, err := root.Open(entry.Name())
		if err != nil {
			continue
		}
		digest := sha256.New()
		size, copyErr := io.Copy(digest, io.LimitReader(file, 16*1024*1024+1))
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || size > 16*1024*1024 {
			continue
		}
		media := "application/json"
		if strings.HasSuffix(entry.Name(), ".png") {
			media = "image/png"
		}
		if strings.HasSuffix(entry.Name(), ".jsonl") {
			media = "application/x-ndjson"
		}
		result = append(result, &pb.SimulationArtifact{Path: entry.Name(), Url: strings.TrimRight(profile.ArtifactUrl, "/") + "/" + url.PathEscape(runID) + "/" + entry.Name(), Sha256: hex.EncodeToString(digest.Sum(nil)), SizeBytes: uint64(size), MediaType: media})
	}
	return result
}
