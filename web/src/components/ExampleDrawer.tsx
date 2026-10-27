import { Drawer, Table, Tag, Typography } from "antd";

// 现行口径：五个维度，每维 20 分，总分直接相加
const GUIDE = [
  { key: "white", label: "白平衡（匀）", rule: "字内部、笔画与笔画之间的空白要均匀", score: "0~20" },
  { key: "black", label: "黑平衡（稳）", rule: "笔画长度=重量；从中间一分为二，两半的笔画总长度/重量要大致相当", score: "0~20" },
  { key: "variation", label: "参差", rule: "上下左右四面的笔画要有长有短，不能一样齐", score: "0~20" },
  {
    key: "structure",
    label: "结构",
    rule: "左右/上下/半包围/全包围/独体字，按结构逻辑看：哪边笔画多，哪边占位大；独体字参照“满”",
    score: "0~20",
  },
  { key: "control", label: "控笔", rule: "笔画书写有轻重变化（起收笔的粗细、顿挫）", score: "0~20" },
];

const FIGS = [
  { src: "/examples/excellent.png", cap: "图 1｜可以给高分：空白停匀、左右两半重量相当、四周笔画有长有短、结构占位合理、起收笔见轻重" },
  { src: "/examples/medium.png", cap: "图 2｜中等：主要问题在「参差」——短撇没送到格角、长捺偏长，参差与结构各扣几分" },
  { src: "/examples/poor.png", cap: "图 3｜需加油：字缩在一角（结构/白平衡失分），两半重量失衡（黑平衡失分），笔画等粗（控笔失分）" },
  { src: "/examples/invalid.png", cap: "图 4｜不能打分：倾斜、失焦、过曝、含未打码姓名、手指遮挡 → 上报异常" },
];

export default function ExampleDrawer({ open, onClose }: { open: boolean; onClose: () => void }) {
  return (
    <Drawer open={open} onClose={onClose} size="large" title="五维打分口径与看图示例">
      <Table
        size="small"
        pagination={false}
        rowKey="key"
        columns={[
          { title: "维度", dataIndex: "label", width: 118, render: (v: string) => <Tag color="blue">{v}</Tag> },
          { title: "看什么（解读）", dataIndex: "rule" },
          { title: "分值", dataIndex: "score", width: 72 },
        ]}
        dataSource={GUIDE}
        style={{ marginBottom: 18 }}
      />
      <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
        总分 = 五个维度直接相加（满分 100），由系统计算；≥90 优秀 / 80~89 良好 / 70~79 中等 / 60~69 及格 / &lt;60 需加油。
        点评 20~50 字，要指出具体维度上具体的问题（例如“参差：短撇没送到左下角”）。
      </Typography.Paragraph>
      <div className="guide-grid">
        {FIGS.map((f) => (
          <div className="guide-fig" key={f.src}>
            <img src={f.src} alt={f.cap} />
            <div className="cap">{f.cap}</div>
          </div>
        ))}
      </div>
    </Drawer>
  );
}
