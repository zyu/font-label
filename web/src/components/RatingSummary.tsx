import { Space, Tag, Typography } from "antd";
import type { Dim } from "../api/types";
import type { Level } from "../lib/annotationLevels";

type Props = {
  dimensions: Dim[];
  grades?: Record<string, Level>;
  comments?: Record<string, string>;
};

export default function RatingSummary({ dimensions, grades, comments }: Props) {
  const visible = dimensions.filter(({ key }) => comments ? comments[key] : grades?.[key]);
  if (!visible.length) return <Typography.Text type="secondary">—</Typography.Text>;
  if (comments) {
    return <Space orientation="vertical" size={2} className="rating-comments">
      {visible.map(({ key, label }) => <Typography.Text key={key} ellipsis={{ tooltip: comments[key] }}>
        {label}：{comments[key]}
      </Typography.Text>)}
    </Space>;
  }
  return <Space wrap size={2}>
    {visible.map(({ key, label }) => <Tag key={key}>{label} {grades?.[key]}</Tag>)}
  </Space>;
}
