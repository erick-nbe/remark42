export const EXPANDED_REPLIES_SET = 'EXPANDED_REPLIES_SET' as const;
export const EXPANDED_REPLIES_TOGGLE = 'EXPANDED_REPLIES_TOGGLE' as const;

export interface ExpandedRepliesSetAction {
  type: typeof EXPANDED_REPLIES_SET;
  id: string;
  expanded: boolean;
}

export interface ExpandedRepliesToggleAction {
  type: typeof EXPANDED_REPLIES_TOGGLE;
  id: string;
}

export type EXPANDED_REPLIES_ACTIONS = ExpandedRepliesSetAction | ExpandedRepliesToggleAction;
