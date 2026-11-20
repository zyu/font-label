import { useEffect, useState } from "react";
import { Modal, Radio, Typography } from "antd";

interface Props {
  open: boolean;
  reasons: { key: string; label: string }[];
  onCancel: () => void;
  onSubmit: (reason: string) => void;
  loading?: boolean;
}

export default function ExceptionPanel({ open, reasons, onCancel, onSubmit, loading }: Props) {
  const [picked, setPicked] = useState<string>();
  const [error, setError] = useState(false);

  useEffect(() => {
    if (open) {
      setPicked(undefined);
      setError(false);
    }
  }, [open]);

  return (
    <Modal className="annotation-exception-modal" open={open} title="标记照片不合格"
      okText="提交照片不合格" cancelText="取消" confirmLoading={loading}
      onCancel={onCancel}
      onOk={() => {
        if (picked && reasons.some((reason) => reason.key === picked)) onSubmit(picked);
        else setError(true);
      }}
      destroyOnHidden
    >
      <Typography.Paragraph type="secondary">
        选择一种异常类型并提交审核。
      </Typography.Paragraph>
      <Radio.Group aria-label="异常类型" value={picked} disabled={loading}
        onChange={(event) => { setPicked(event.target.value); setError(false); }}
        style={{ display: "flex", flexDirection: "column", gap: 10 }}
        options={reasons.map((reason) => ({ label: reason.label, value: reason.key }))}
      />
      {(error || reasons.length === 0) && (
        <div className="exception-validation" role="alert" data-test="exception-error">
          {reasons.length === 0 ? "暂无可选异常类型，请联系管理员。" : "请选择一种异常类型"}
        </div>
      )}
    </Modal>
  );
}
