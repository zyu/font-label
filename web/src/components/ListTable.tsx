import { Table, type TableProps } from "antd";

export default function ListTable<T extends object>({ pagination, scroll, ...props }: TableProps<T>) {
  return (
    <Table<T>
      {...props}
      pagination={pagination === false ? false : { ...pagination, pageSize: Math.min(pagination?.pageSize ?? 20, 20), showSizeChanger: false }}
      scroll={{ x: "max-content", y: "min(48dvh, 480px)", ...scroll }}
    />
  );
}
