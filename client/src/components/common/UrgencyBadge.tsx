import React from "react";
import { View, Text } from "react-native";
import { Colors, cardStyles } from "../../styles/cardStyles";

interface UrgencyBadgeProps {
  score: number;
}

/**
 * UrgencyBadge — color-coded urgency indicator
 * - > 0.8 = red dot + "Urgent"
 * - > 0.5 = orange dot + "Soon"
 * - <= 0.5 = no badge rendered
 */
export const UrgencyBadge: React.FC<UrgencyBadgeProps> = ({ score }) => {
  if (score <= 0.5) return null;

  const isUrgent = score > 0.8;
  const dotColor = isUrgent ? Colors.urgentRed : Colors.urgentOrange;
  const bgColor = isUrgent
    ? Colors.urgentBg
    : "#FDF3E8";
  const label = isUrgent ? "Urgent" : "Soon";
  const textColor = isUrgent ? Colors.urgentRed : Colors.urgentOrange;

  return (
    <View style={[cardStyles.urgencyBadge, { backgroundColor: bgColor }]}>
      <View style={[cardStyles.urgencyDot, { backgroundColor: dotColor }]} />
      <Text style={[cardStyles.urgencyText, { color: textColor }]}>
        {label}
      </Text>
    </View>
  );
};
