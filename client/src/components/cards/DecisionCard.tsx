import React, { forwardRef, useRef, useImperativeHandle, useCallback } from "react";
import { View, Text, findNodeHandle, UIManager, TouchableOpacity } from "react-native";
import { useNavigation } from "@react-navigation/native";
import type { NativeStackNavigationProp } from "@react-navigation/native-stack";
import { cardStyles, Colors } from "../../styles/cardStyles";
import type { DecisionCard as DecisionCardType, EmailAccount } from "../../types/cards";
import { UrgencyBadge } from "../common/UrgencyBadge";
import { CitationChip } from "../common/CitationChip";
import { CardActions } from "./CardActions";
import { AccountBadge } from "../account/AccountBadge";
import type { ChunkCitation } from "../../types/cards";
import type { RootStackParamList } from "../../navigation/AppNavigator";

export interface DecisionCardProps {
  card: DecisionCardType;
  onDecide: (cardId: string) => void;
  onConsult: (cardId: string) => void;
  onSource: (cardId: string) => void;
  onSkip: (cardId: string) => void;
  onPressCitation?: (citation: ChunkCitation) => void;
  /** Optional account info to display source account badge */
  sourceAccount?: Pick<EmailAccount, "id" | "email" | "provider">;
}

/**
 * TutorialTargetRefs — Exposed via ref for tutorial spotlight targeting
 */
export interface DecisionCardTutorialTargets {
  /** The card shell (card_body target) */
  cardBody: View | null;
  /** The Source button wrapper (source_button target) */
  sourceButton: View | null;
  /** The Decide/primary button wrapper (decision_input + approve_button target) */
  decideButton: View | null;
}

/**
 * DecisionCard — Main card component
 *
 * Layout (top to bottom):
 *   Source account   — small tag showing which email account (multi-account)
 *   From field       — name, relationship context, interaction count
 *   They want        — prominent summary
 *   Context          — bullets (prior commitments, quoted numbers, sentiment)
 *   Need from user   — call-to-action styled block
 *   Citations        — clickable chunk_id chips
 *   Urgency badge    — color-coded if > 0.5
 *   Actions          — [Decide] [Consult] [Source] [Skip]
 *
 * Tutorial integration:
 *   - Uses forwardRef to expose internal View refs for spotlight targeting
 *   - CardActions ref is captured to target the Decide button
 *   - Source button is wrapped in a View for targeting
 *
 * Multi-account support:
 *   - When sourceAccount prop is provided, shows a colored badge at the top
 *   - Badge displays provider icon (G/O) + Gmail/Outlook label
 *   - Helps users distinguish decisions from work vs personal accounts
 */
export const DecisionCard = forwardRef<DecisionCardTutorialTargets, DecisionCardProps>(
  ({ card, onDecide, onConsult, onSource, onSkip, onPressCitation, sourceAccount }, forwardedRef) => {
    const from = card.from;
    const ctx = card.context;
    const navigation = useNavigation<NativeStackNavigationProp<RootStackParamList>>();

    // Navigate to contact profile on sender name tap
    const handlePressSender = useCallback(() => {
      // Derive a stable contactId from the email hash
      const contactId = card.thread_id
        ? card.thread_id.split("-")[0] + "-contact"
        : from.email.replace(/[^a-zA-Z0-9]/g, "-");
      navigation.navigate("ContactProfile", {
        contactId,
        contactName: from.name,
        contactEmail: from.email,
      });
    }, [from.name, from.email, card.thread_id, navigation]);

    // Internal refs for tutorial targeting
    const cardBodyRef = useRef<View>(null);
    const sourceButtonRef = useRef<View>(null);
    const decideButtonRef = useRef<View>(null);

    // Expose refs to parent for tutorial spotlight measurement
    useImperativeHandle(
      forwardedRef,
      () => ({
        cardBody: cardBodyRef.current,
        sourceButton: sourceButtonRef.current,
        decideButton: decideButtonRef.current,
      }),
      []
    );

    // Build context bullet points
    const bullets: string[] = [];
    if (ctx.prior_commitments && ctx.prior_commitments.length > 0) {
      bullets.push(...ctx.prior_commitments);
    }
    if (ctx.quoted_numbers && ctx.quoted_numbers.length > 0) {
      bullets.push(...ctx.quoted_numbers);
    }
    if (ctx.deadlines && ctx.deadlines.length > 0) {
      bullets.push(...ctx.deadlines);
    }

    return (
      <View ref={cardBodyRef} style={cardStyles.cardShell} collapsable={false}>
        {/* ── SOURCE ACCOUNT BADGE ── */}
        {sourceAccount && (
          <View style={{ marginBottom: 6, marginTop: -4 }}>
            <AccountBadge
              account={sourceAccount}
              displayName={sourceAccount.email.split("@")[0]}
              size="sm"
            />
          </View>
        )}

        {/* ── HEADER: From ── */}
        <View style={cardStyles.cardHeader}>
          <TouchableOpacity onPress={handlePressSender} activeOpacity={0.7}>
            <Text style={cardStyles.fromName}>{from.name}</Text>
          </TouchableOpacity>
          {from.relationship_context && (
            <Text style={cardStyles.fromContext}>{from.relationship_context}</Text>
          )}
          {from.interaction_count > 0 && (
            <Text style={cardStyles.fromMeta}>
              {`${from.interaction_count} interaction${from.interaction_count !== 1 ? "s" : ""}`}
              {from.last_contact ? ` · ${from.last_contact}` : ""}
            </Text>
          )}
          <UrgencyBadge score={card.urgency_score} />
        </View>

        {/* ── THEY WANT ── */}
        <View style={cardStyles.cardSection}>
          <Text style={cardStyles.sectionLabel}>They want</Text>
          <Text style={cardStyles.theyWantText}>{card.they_want}</Text>
        </View>

        {/* ── CONTEXT ── */}
        {bullets.length > 0 && (
          <View style={cardStyles.cardSection}>
            <Text style={cardStyles.sectionLabel}>Context</Text>
            {bullets.map((b, i) => (
              <Text key={`bullet-${i}`} style={cardStyles.contextBullet}>
                {"• "}{b}
              </Text>
            ))}
            {ctx.sentiment && (
              <Text style={cardStyles.contextSentiment}>
                {`Sentiment: ${ctx.sentiment}`}
              </Text>
            )}
          </View>
        )}

        {/* ── NEED FROM USER ── */}
        <View style={cardStyles.cardSection}>
          <Text style={cardStyles.sectionLabel}>Need from you</Text>
          <View style={cardStyles.needContainer}>
            <Text style={cardStyles.needText}>{card.need_from_user}</Text>
          </View>
        </View>

        {/* ── CITATIONS ── */}
        {card.chunk_citations.length > 0 && (
          <View style={cardStyles.cardSection}>
            <Text style={cardStyles.sectionLabel}>Sources</Text>
            <View ref={sourceButtonRef} collapsable={false}>
              <CitationChip
                citations={card.chunk_citations}
                onPressCitation={(c) => onPressCitation?.(c) ?? onSource(card.id)}
              />
            </View>
          </View>
        )}

        {/* ── ACTIONS ── */}
        <CardActions
          ref={decideButtonRef}
          onDecide={() => onDecide(card.id)}
          onConsult={() => onConsult(card.id)}
          onSource={() => onSource(card.id)}
          onSkip={() => onSkip(card.id)}
        />
      </View>
    );
  }
);

DecisionCard.displayName = "DecisionCard";
