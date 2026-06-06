import React from "react";
import { View, Text, TouchableOpacity } from "react-native";
import { Colors, cardStyles } from "../../styles/cardStyles";

interface ErrorFallbackProps {
  message?: string;
  onRetry?: () => void;
}

export const ErrorFallback: React.FC<ErrorFallbackProps> = ({
  message = "Something went wrong.",
  onRetry,
}) => (
  <View style={cardStyles.screenCenter}>
    <Text style={cardStyles.errorText}>{message}</Text>
    {onRetry && (
      <TouchableOpacity onPress={onRetry} style={cardStyles.retryBtn}>
        <Text style={cardStyles.retryBtnText}>Try Again</Text>
      </TouchableOpacity>
    )}
  </View>
);
