import { useEffect, useState } from "react";
import { Modal, Checkbox, Input, Typography } from "antd";

interface Props {
  open: boolean;
  reasons: { key: string; label: string }[];
  initial?: { reasons: string[]; note: string };
  onCancel: () => void;
  onSubmit: (reasons: string[], note: string) => void;
  loading?: boolean;
}

export default function ExceptionPanel({ open, reasons, initial, onCancel, onSubmit, loading }: Props) {
  const [picked, setPicked] = useState<string[]>([]);
  const [note, setNote] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    if (open) {
      setPicked(initial?.reasons ?? []);
      setNote(initial?.note ?? "");
      setError("");
    }
  }, [open, initial]);

  return (
    <Modal
      open={open}
      title="标记照片不合格"
      okText="提交照片不合格"
      cancelText="取消"
      confirmLoading={loading}
      onCancel={onCancel}
      onOk={() => {
        if (picked.length === 0) {
          setError("请至少选择一项照片不合格原因");
          return;
        }
        onSubmit(picked, note);
      }}
      destroyOnHidden
    >
      <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
        请勾选照片不合格的原因，可补充说明。提交后交给审核员确认，无需填写分数和点评。
      </Typography.Paragraph>
      <Checkbox.Group
        value={picked}
        onChange={(v) => {
          setPicked(v as string[]);
          setError("");
        }}
        style={{ display: "flex", flexDirection: "column", gap: 10 }}
        options={reasons.map((r) => ({ label: r.label, value: r.key }))}
      />
      {error && (
        <div role="alert" style={{ color: "#cf1322", fontSize: 12, marginTop: 10 }} data-test="exception-error">
          {error}
        </div>
      )}
      <Input.TextArea
        style={{ marginTop: 14 }}
        rows={3}
        maxLength={200}
        showCount
        placeholder="补充说明（可选）"
        value={note}
        onChange={(e) => setNote(e.target.value)}
      />
    </Modal>
  );
}
