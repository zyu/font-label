import type { Level } from "../lib/annotationLevels";

export type Role = "admin" | "reviewer" | "annotator" | "quality";

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
}
export interface Rubric {
  id: number;
  name: string;
  dims: Dim[];
  levels: Level[];
  dim_comment_min: number;
  dim_comment_max: number;
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
  comment_templates: (string | { dimension: string; grade: string; text: string })[];
  limits: Limits;
}

export type ImageStatus =
  | "unassigned"
  | "assigned"
  | "submitted"
  | "approved"
  | "rejected"
  | "excluded"
  | "exception_locked";

export interface ImageDTO {
  group_id?: number;
  group?: string;
  sample_id?: string;
  quality_pending?: boolean;
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
  comment: string;
  grades: Record<string, Level>;
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

export interface ReviewQueueItem extends ImageDTO {
  review_id?: number;
  reviewed_at?: string;
  review_result?: "approved" | "rejected" | "excluded";
  can_review?: boolean;
}

export interface Review {
  source?: "review" | "quality" | "arbitration";
  id: number;
  action: "approve" | "reject";
  comment: string;
  reason_tags: string[];
  reviewer: string;
  grade_patch?: Record<string, Level>;
  created_at: string;
  annotation_version: number;
}

export interface ImageDetail {
  image: ImageDTO;
  reviews: Review[];
  draft: Draft | null;
  grade_changes?: Record<string, { from: Level; to: Level }>;
}

export interface Draft {
  grades?: Record<string, Level>;
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
  exception_locked: "对组异常已确认",
};

export const STATUS_COLOR: Record<string, string> = {
  unassigned: "default",
  assigned: "processing",
  submitted: "warning",
  approved: "success",
  rejected: "error",
  excluded: "default",
  exception_locked: "default",
};
