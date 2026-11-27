package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/shangyue/font-label/server/internal/platform/httpx"
	"github.com/shangyue/font-label/server/internal/platform/score"
)

// Every mutation that can change another group's task takes the source lock
// before any task row lock. Dispatch already locks the source rows.
func lockTaskSource(ctx context.Context, tx pgx.Tx, taskID int64) (int64, error) {
	var sourceID int64
	err := tx.QueryRow(ctx, `
		SELECT src.id FROM images task JOIN images src ON src.id=task.source_image_id
		WHERE task.id=$1 FOR NO KEY UPDATE OF src`, taskID).Scan(&sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return sourceID, err
}

func exceptionLockedError() *httpx.APIError {
	return httpx.New(http.StatusConflict, "EXCEPTION_LOCKED", "对组异常已确认，此图无需标注")
}

type gradeDifference struct {
	Dimension     string `json:"dimension"`
	Label         string `json:"label"`
	OwnGrade      string `json:"own_grade"`
	PeerGrade     string `json:"peer_grade"`
	Gap           int    `json:"gap"`
	OwnComment    string `json:"own_comment"`
	PeerComment   string `json:"peer_comment"`
	PeerGroup     string `json:"peer_group"`
	PeerAnnotator string `json:"peer_annotator"`
}

func groupGradeDifferences(ctx context.Context, tx pgx.Tx, sourceID, taskID int64, rubric score.Rubric, grades map[string]string, comments map[string]string) ([]gradeDifference, error) {
	rows, err := tx.Query(ctx, `
		SELECT g.name, u.display_name, COALESCE(v.payload->'grades',a.grades), COALESCE(v.payload->'dim_comments',a.dim_comments)
		FROM images peer
		JOIN annotation_groups g ON g.id=peer.group_id
		JOIN annotations a ON a.image_id=peer.id AND a.version>peer.voided_through_version
		JOIN users u ON u.id=a.annotator_id
		LEFT JOIN annotation_versions v ON v.image_id=peer.id AND v.version=a.version
		WHERE peer.source_image_id=$1 AND peer.id<>$2
		  AND peer.status IN ('submitted','approved') AND a.kind='score'
		ORDER BY a.submitted_at, peer.id`, sourceID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var differences []gradeDifference
	for rows.Next() {
		var group, annotator string
		var peerGrades, peerComments map[string]string
		if err := rows.Scan(&group, &annotator, &peerGrades, &peerComments); err != nil {
			return nil, err
		}
		for _, dim := range rubric.Dims {
			ownGrade, peerGrade := grades[dim.Key], peerGrades[dim.Key]
			own, peer := score.GradeRank(ownGrade), score.GradeRank(peerGrade)
			if peer == 0 {
				continue
			}
			gap := max(own-peer, peer-own)
			if gap < 2 {
				continue
			}
			differences = append(differences, gradeDifference{
				Dimension: dim.Key, Label: dim.Label, OwnGrade: ownGrade, PeerGrade: peerGrade,
				Gap: gap, OwnComment: comments[dim.Key], PeerComment: peerComments[dim.Key], PeerGroup: group,
				PeerAnnotator: annotator,
			})
		}
	}
	return differences, rows.Err()
}

func withPeerAnnotators(response map[string]any, differences []gradeDifference) map[string]any {
	var names []string
	for _, difference := range differences {
		if !slices.Contains(names, difference.PeerAnnotator) {
			names = append(names, difference.PeerAnnotator)
		}
	}
	if len(names) > 0 {
		response["peer_annotators"] = names
	}
	return response
}

// checkGroupKinds requires explicit acceptance of the exact peer submissions
// seen in the warning. A new peer submission invalidates an earlier acceptance.
func checkGroupKinds(ctx context.Context, tx pgx.Tx, sourceID, taskID int64, kind, accepted string) error {
	rows, err := tx.Query(ctx, `
		SELECT peer.id,a.version,g.name,a.kind,COALESCE(a.grades,'{}'),
		       a.dim_comments,a.exception_reasons,a.comment
		FROM images peer
		JOIN annotation_groups g ON g.id=peer.group_id
		JOIN annotations a ON a.image_id=peer.id AND a.version>peer.voided_through_version
		WHERE peer.source_image_id=$1 AND peer.id<>$2
		  AND peer.status IN ('submitted','approved') AND a.kind<>$3
		ORDER BY peer.id`, sourceID, taskID, kind)
	if err != nil {
		return err
	}
	defer rows.Close()
	type peerResult struct {
		TaskID   int64             `json:"task_id"`
		Version  int               `json:"version"`
		Group    string            `json:"group"`
		Kind     string            `json:"kind"`
		Grades   map[string]string `json:"grades"`
		Comments map[string]string `json:"dim_comments"`
		Reasons  []string          `json:"exception_reasons"`
		Note     string            `json:"exception_note"`
	}
	var peers []peerResult
	for rows.Next() {
		var peer peerResult
		if err := rows.Scan(&peer.TaskID, &peer.Version, &peer.Group, &peer.Kind, &peer.Grades, &peer.Comments, &peer.Reasons, &peer.Note); err != nil {
			return err
		}
		peers = append(peers, peer)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(peers) == 0 {
		return nil
	}
	data, err := json.Marshal(peers)
	if err != nil {
		return err
	}
	// Include the local task and submission kind so acknowledgements are
	// scoped to this direction of the conflict.
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s", taskID, kind, data)))
	token := hex.EncodeToString(sum[:])
	if accepted == token {
		return nil
	}
	conflict := httpx.New(http.StatusConflict, "GROUP_KIND_MISMATCH", "两组对图片是否有效的判定不同")
	conflict.Details = map[string]any{"peers": peers, "confirmation": token}
	return conflict
}
