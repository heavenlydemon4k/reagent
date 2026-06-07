import React from "react";
import { View, ActivityIndicator, Text } from "react-native";
import { Colors, cardStyles } from "../../styles/cardStyles";

export const LoadingSpinner: React.FC<{ message?: string }> = ({
  message = "Loading...",
}) => (
  <View style={cardStyles.screenCenter}>
    <ActivityIndicator size="large" color={Colors.primary} />
    {message ? (
      <Text style={cardStyles.loadingText}>{message}</Text>
    ) : null}
  </View>
);

export const InlineSpinner: React.FC = () => (
  <View style={{ padding: 24, alignItems: "center" }}>
    <ActivityIndicator size="small" color={Colors.primary} />
  </View>
);
