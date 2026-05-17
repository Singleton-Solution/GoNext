/**
 * The minimal Lexical node-set that `<RichText/>` mounts.
 *
 * We deliberately stay narrow: every node here corresponds to something the
 * canonical inline model (`InlineRun`) can express, plus the block-level
 * structural nodes the rich-text plugin requires to render at all
 * (`ParagraphNode`, `HeadingNode`, `QuoteNode`, `List…Node`).
 *
 * Anything richer — tables, mentions, equations, embeds — belongs in a
 * plugin or a higher-level package layered on top. Keeping the bundle
 * lean here means the per-block edit surface costs ~tens of KB rather
 * than the full ~hundreds of KB the Lexical demo ships.
 */
import type { Klass, LexicalNode, LexicalNodeReplacement } from 'lexical';
import { HeadingNode, QuoteNode } from '@lexical/rich-text';
import { ListItemNode, ListNode } from '@lexical/list';
import { LinkNode } from '@lexical/link';

export const RICH_TEXT_NODES: ReadonlyArray<Klass<LexicalNode> | LexicalNodeReplacement> = [
  HeadingNode,
  QuoteNode,
  ListNode,
  ListItemNode,
  LinkNode,
];
