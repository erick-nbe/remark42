import { h, FunctionComponent } from 'preact';
import { shallowEqual } from 'react-redux';
import { useCallback, useEffect, useRef } from 'preact/hooks';
import b from 'bem-react-helper';
import { useIntl, defineMessages, FormattedMessage } from 'react-intl';

import { Comment as CommentInterface } from 'common/types';
import { getHandleClickProps } from 'common/accessibility';
import { StoreState, useAppDispatch, useAppSelector } from 'store';
import { setCollapse } from 'store/thread/actions';
import { getThreadIsCollapsed } from 'store/thread/getters';
import { toggleExpandedReplies } from 'store/expanded-replies/actions';
import { InView } from 'components/root/in-view/in-view';
import { ConnectedComment as Comment } from 'components/comment/connected-comment';
import { CommentForm } from 'components/comment-form';

interface Props {
  id: CommentInterface['id'];
  childs?: CommentInterface['id'][];
  level: number;
  mix?: string;
  parentAuthor?: string;
  parentUserId?: string;
  parentUserPicture?: string;

  getPreview(text: string): Promise<string>;
}

const messages = defineMessages({
  replies: {
    id: 'thread.replies',
    defaultMessage: '{count, plural, one {# reply} other {# replies}}',
  },
});

// Recursive function to count all nested replies
const countAllReplies = (
  commentId: string,
  childComments: Record<string, string[]>
): number => {
  const directChildren = childComments[commentId] || [];
  let total = directChildren.length;
  for (const childId of directChildren) {
    total += countAllReplies(childId, childComments);
  }
  return total;
};

// Recursive function to get all nested reply IDs flattened
const getAllReplyIds = (
  commentId: string,
  childComments: Record<string, string[]>,
  allComments: Record<string, CommentInterface>
): string[] => {
  const directChildren = childComments[commentId] || [];
  const result: string[] = [];
  
  for (const childId of directChildren) {
    result.push(childId);
    const nestedReplies = getAllReplyIds(childId, childComments, allComments);
    result.push(...nestedReplies);
  }
  
  // Sort by time ascending (oldest first, like YouTube)
  result.sort((a, b) => {
    const timeA = new Date(allComments[a]?.time || 0).getTime();
    const timeB = new Date(allComments[b]?.time || 0).getTime();
    return timeA - timeB;
  });
  
  return result;
};

const commentSelector = (id: string) => (state: StoreState) => {
  const { theme, comments, expandedReplies } = state;
  const { allComments, childComments } = comments;
  const comment = allComments[id];
  const childs = childComments[id];
  const collapsed = getThreadIsCollapsed(comment)(state);
  const repliesExpanded = expandedReplies[id] || false;

  return { comment, childs, collapsed, theme, allComments, childComments, repliesExpanded };
};

export const Thread: FunctionComponent<Props> = ({ id, level, mix, getPreview, parentAuthor, parentUserId, parentUserPicture }) => {
  const dispatch = useAppDispatch();
  const intl = useIntl();
  const { collapsed, comment, childs, theme, allComments, childComments, repliesExpanded } = useAppSelector(commentSelector(id), shallowEqual);
  
  const collapse = useCallback(() => {
    dispatch(setCollapse(id, !collapsed));
  }, [id, collapsed, dispatch]);

  const toggleReplies = useCallback(() => {
    dispatch(toggleExpandedReplies(id));
  }, [id, dispatch]);

  if (comment.hidden) return null;

  const isTopLevel = level === 0;
  const indented = level > 0;
  
  // For top-level comments, count ALL nested replies
  const totalRepliesCount = isTopLevel ? countAllReplies(id, childComments) : (childs ? childs.length : 0);
  
  // For top-level comments, get all replies flattened and sorted by most recent
  const allRepliesFlattened = isTopLevel ? getAllReplyIds(id, childComments, allComments) : [];

  // YouTube-style: top-level comments show replies collapsed with "▼ X replies" button
  if (isTopLevel) {
    return (
      <div
        className={b('thread', { mix }, { level, theme, indented: false, youtube: true })}
        role={['listitem'].concat(repliesExpanded && totalRepliesCount > 0 ? 'list' : []).join(' ')}
        aria-expanded={repliesExpanded}
      >
        <InView>
          {(inviewProps) => (
            <Comment
              CommentForm={CommentForm}
              ref={inviewProps.ref}
              key={`comment-${id}`}
              view="main"
              intl={intl}
              data={comment}
              repliesCount={totalRepliesCount}
              level={0}
              inView={inviewProps.inView}
            />
          )}
        </InView>

        {totalRepliesCount > 0 && (
          <button
            className={b('thread__replies-toggle', { mods: { expanded: repliesExpanded } })}
            onClick={toggleReplies}
            type="button"
          >
            <svg
              className={b('thread__replies-toggle-icon')}
              viewBox="0 0 24 24"
              width="20"
              height="20"
              fill="currentColor"
            >
              <path d="M7.41 8.59L12 13.17l4.59-4.58L18 10l-6 6-6-6 1.41-1.41z" />
            </svg>
            <span>{intl.formatMessage(messages.replies, { count: totalRepliesCount })}</span>
          </button>
        )}

        {repliesExpanded && totalRepliesCount > 0 && (
          <div className={b('thread__replies-container')}>
            {allRepliesFlattened.map((replyId) => {
              const replyComment = allComments[replyId];
              if (!replyComment || replyComment.hidden) return null;
              
              // Only show @mention if replying to another reply (not to the root comment)
              // i.e., the parent of this reply is NOT the top-level comment
              const isReplyToReply = replyComment.pid !== id;
              
              // Find the parent comment to get the author name, ID, and picture for @mention
              const parentComment = isReplyToReply ? allComments[replyComment.pid] : undefined;
              const replyToAuthor = parentComment ? parentComment.user.name : undefined;
              const replyToUserId = parentComment ? parentComment.user.id : undefined;
              const replyToUserPicture = parentComment ? parentComment.user.picture : undefined;
              
              return (
                <Thread
                  key={`reply-${replyId}`}
                  id={replyId}
                  level={1}
                  getPreview={getPreview}
                  parentAuthor={replyToAuthor}
                  parentUserId={replyToUserId}
                  parentUserPicture={replyToUserPicture}
                />
              );
            })}
          </div>
        )}
      </div>
    );
  }

  // Non-top-level (replies) - render without nesting, just the comment
  return (
    <div
      className={b('thread', { mix }, { level: 1, theme, indented: false, reply: true })}
      role="listitem"
    >
      <InView>
        {(inviewProps) => (
          <Comment
            CommentForm={CommentForm}
            ref={inviewProps.ref}
            key={`comment-${id}`}
            view="main"
            intl={intl}
            data={comment}
            repliesCount={0}
            level={1}
            inView={inviewProps.inView}
            parentAuthor={parentAuthor}
            parentUserId={parentUserId}
            parentUserPicture={parentUserPicture}
          />
        )}
      </InView>
    </div>
  );
};
