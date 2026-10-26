export type Role = "admin" | "reviewer" | "annotator";

export interface User {
  id: number;
  username: string;
  display_name: string;
  role: Role;
  must_change_pwd?: boolean;
  status?: string;
}

export interface Dim {
  key: string;
  label: string;
  weight: number;
  max: number;
  /** 可选：打分步长（5 = 只能 0/5/10/15/20） */
  step?: number;
}

export interface Band {
  label: string;
  min: number;
}

export interface Rubric {
  id: number;
  name: string;
  dims: Dim[];
  grade_bands: Band[];
  /** 旧：单条总点评的字数范围（保留兼容，界面已改为按维度点评） */
  comment_min: number;
  comment_max: number;
  /** 每个维度的点评字数范围（现行：10~50 字） */
  dim_comment_min: number;
  dim_comment_max: number;
  allow_total_override: boolean;
}

export interface Limits {
  max_hold_per_user: number;
  lock_ttl_hours: number;
  claim_default_count: number;
  max_claim_count: number;
  max_upload_files: number;
  max_file_mb: number;
  auto_next: boolean;
}

export interface MeResponse {
  user: User;
  rubric: Rubric;
  exception_reasons: { key: string; label: string }[];
  reject_reason_tags: string[];
  comment_templates: string[];
  limits: Limits;
}

export type ImageStatus =
  | "unassigned"
  | "assigned"
  | "submitted"
  | "approved"
  | "rejected"
  | "excluded";

export interface ImageDTO {
  id: number;
  folder_id: number;
  folder: string;
  stored_name: string;
  orig_name: string;
  seq: number;
  day: string;
  status: ImageStatus;
  width: number;
  height: number;
  annotator_id: number | null;
  annotator: string;
  reject_count: number;
  review_count: number;
  kind: "score" | "exception" | "";
  total: number | null;
  grade: string;
  comment: string;
  scores: Record<string, number>;
  /** 五维各自的点评：{ white: "...", black: "..." } */
  dim_comments: Record<string, string>;
  exception_reasons: string[];
  duration_ms: number | null;
  claimed_at: string | null;
  submitted_at: string | null;
  thumb_url: string;
  preview_url: string;
  original_url: string;
}

export interface Review {
  id: number;
  action: "approve" | "reject";
  comment: string;
  reason_tags: string[];
  reviewer: string;
  score_patch?: Record<string, number>;
  created_at: string;
  annotation_version: number;
}

export interface ImageDetail {
  image: ImageDTO;
  reviews: Review[];
  draft: Draft | null;
}

export interface Draft {
  scores?: Record<string, number>;
  /** 五维各自的点评 */
  dim_comments?: Record<string, string>;
  comment?: string;
  kind?: "score" | "exception";
  exception_reasons?: string[];
  exception_note?: string;
}

export interface FolderDTO {
  id: number;
  name: string;
  note: string;
  created_at: string;
  creator: string;
  archived: boolean;
  total: number;
  unassigned: number;
  assigned: number;
  submitted: number;
  approved: number;
  rejected: number;
  excluded: number;
}

export interface Page<T> {
  items: T[];
  total: number;
  page: number;
  page_size: number;
}

export interface ClaimResult {
  granted: number;
  requested: number;
  remaining_in_pool: number;
  images: { id: number; stored_name: string; folder_id: number; seq: number }[];
  hold: { current: number; limit: number };
}

export const STATUS_LABEL: Record<string, string> = {
  unassigned: "待领取",
  assigned: "已领取",
  submitted: "待审核",
  approved: "已通过",
  rejected: "已打回",
  excluded: "无效图",
};

export const STATUS_COLOR: Record<string, string> = {
  unassigned: "default",
  assigned: "processing",
  submitted: "warning",
  approved: "success",
  rejected: "error",
  excluded: "default",
};
