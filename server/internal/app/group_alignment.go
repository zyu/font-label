package app

import (
	"context"
	"errors"
	"net/http"

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
	Dimension   string `json:"dimension"`
	Label       string `json:"label"`
	OwnGrade    string `json:"own_grade"`
	PeerGrade   string `json:"peer_grade"`
	Gap         int    `json:"gap"`
	OwnComment  string `json:"own_comment"`
	PeerComment string `json:"peer_comment"`
	PeerGroup   string `json:"peer_group"`
}

func checkGroupGrades(ctx context.Context, tx pgx.Tx, sourceID, taskID int64, rubric score.Rubric, grades map[string]string, comments map[string]string) error {
	rows, err := tx.Query(ctx, `
		SELECT g.name, COALESCE(v.payload->'grades',a.grades), COALESCE(v.payload->'dim_comments',a.dim_comments)
		FROM images peer
		JOIN annotation_groups g ON g.id=peer.group_id
		JOIN annotations a ON a.image_id=peer.id AND a.version>peer.voided_through_version
		LEFT JOIN annotation_versions v ON v.image_id=peer.id AND v.version=a.version
		WHERE peer.source_image_id=$1 AND peer.id<>$2
		  AND peer.status IN ('submitted','approved') AND a.kind='score'
		ORDER BY a.submitted_at, peer.id`, sourceID, taskID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var differences []gradeDifference
	for rows.Next() {
		var group string
		var peerGrades, peerComments map[string]string
		if err := rows.Scan(&group, &peerGrades, &peerComments); err != nil {
			return err
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
			})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(differences) == 0 {
		return nil
	}
	conflict := httpx.New(http.StatusConflict, "GRADE_MISMATCH", "组间等级差异")
	conflict.Details = map[string]any{"differences": differences}
	return conflict
}
