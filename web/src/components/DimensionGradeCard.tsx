import { useId, type KeyboardEvent } from "react";
import { Button, Dropdown, Input } from "antd";
import { DownOutlined } from "@ant-design/icons";
import type { Dim } from "../api/types";
import type { DimCommentState } from "../lib/dimComments";
import { LEVELS, commentChoices, levelForScore, levelGuide, scoreForLevel, type Level } from "../lib/annotationLevels";

interface Props {
  dim: Dim;
  score?: number;
  comment: DimCommentState;
  templates: unknown;
  minLength: number;
  maxLength: number;
  editable: boolean;
  frozen: boolean;
  active: boolean;
  invalid: boolean;
  onActivate: () => void;
  onPick: (grade: Level) => void;
  onComment: (text: string) => void;
}

const gradeMovement: Record<string, number | undefined> = { ArrowLeft: -1, ArrowUp: -1, ArrowRight: 1, ArrowDown: 1 };
const shortNames: Record<string, string> = { white: "匀", black: "稳" };

export default function DimensionGradeCard({
  dim, score, comment, templates, minLength, maxLength, editable, frozen, active, invalid,
  onActivate, onPick, onComment,
}: Props) {
  const id = useId();
  const grade = levelForScore(dim, score);
  const guide = grade && levelGuide[grade];
  const levels = LEVELS.filter((level) => levelForScore(dim, scoreForLevel(dim, level)) === level);
  const canWrite = editable && !frozen && !!grade;
  const choices = grade ? commentChoices(templates, dim.key, grade) : [];
  const shortName = shortNames[dim.key];

  function moveGrade(event: KeyboardEvent<HTMLDivElement>, currentLevel: Level) {
    const offset = gradeMovement[event.key];
    if (offset === undefined || !editable || frozen || levels.length === 0) return;
    event.preventDefault();
    const index = levels.indexOf(currentLevel);
    const next = levels[(index + offset + levels.length) % levels.length];
    onPick(next);
    event.currentTarget.querySelector<HTMLButtonElement>(`[data-level="${next}"]`)?.focus();
  }

  return (
    <section className="dimension-card" data-dim-comment={dim.key} data-active={active} data-invalid={invalid}
      onFocus={onActivate} aria-labelledby={id}>
      <div className="dimension-card-header">
        <div>
          <h3 id={id}>{shortName ?? dim.label}</h3>
          {shortName && <span className="dimension-subtitle">{dim.label.replace(/\s*[（(][匀稳][）)]$/, "")}</span>}
        </div>
        <div className="dimension-grades" role="radiogroup" aria-label={`${dim.label}定级`} aria-required={editable}
          onKeyDown={(event) => {
            const level = (event.target as HTMLButtonElement).dataset.level as Level | undefined;
            if (level) moveGrade(event, level);
          }}>
          {LEVELS.map((level) => (
            <button key={level} type="button" role="radio" aria-checked={level === grade} data-level={level}
              title={!levels.includes(level) ? "当前评分方案未配置此档" : levelGuide[level].title}
              tabIndex={(grade ?? levels[0]) === level ? 0 : -1}
              disabled={!editable || frozen || !levels.includes(level)} onClick={() => onPick(level)}>
              {level}
            </button>
          ))}
        </div>
        {editable && <span className="dimension-required">评语必填</span>}
      </div>
      {guide && (
        <div className="dimension-guide" aria-live="polite">
          <strong>{grade} {guide.title}</strong>
          <span>{guide.rule}</span>
        </div>
      )}
      <Input.TextArea
        aria-label={`${dim.label}评语`}
        aria-invalid={invalid && canWrite}
        aria-describedby={editable ? `${id}-hint` : undefined}
        disabled={editable && !canWrite}
        readOnly={!editable}
        status={invalid && canWrite ? "error" : undefined}
        rows={2}
        maxLength={maxLength}
        value={comment.text}
        placeholder={!editable ? "暂无评语" : !grade ? "请先选择等级" : grade === "S" ? "写清这一维做得好在哪里" : "指出这一维的具体问题"}
        data-test={`dim-comment-${dim.key}`}
        onChange={(event) => onComment(event.target.value)}
      />
      {editable && (
        <div className="dimension-card-footer">
          <span id={`${id}-hint`} data-test={`dim-comment-hint-${dim.key}`} data-valid={comment.ok}>
            {grade ? comment.hint : invalid ? "请选择等级" : `${minLength}～${maxLength} 字`}
          </span>
          <Dropdown trigger={["click"]} disabled={!canWrite || !choices.length}
            menu={{
              items: choices.map((text, index) => ({ key: String(index), label: text })),
              onClick: ({ key }) => onComment(comment.text + choices[Number(key)]),
            }}>
            <Button disabled={!canWrite || !choices.length} size="small" aria-label={`${dim.label}常用评语`} icon={<DownOutlined />} iconPlacement="end">
              常用评语
            </Button>
          </Dropdown>
        </div>
      )}
    </section>
  );
}
