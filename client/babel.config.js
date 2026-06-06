module.exports = function (api) {
  api.cache(true);
  return {
    presets: ['babel-preset-expo'],
    plugins: [
      [
        'module-resolver',
        {
          root: ['./src'],
          alias: {
            '@': './src',
            '@types': './src/types',
            '@stores': './src/stores',
            '@services': './src/services',
            '@hooks': './src/hooks',
            '@navigation': './src/navigation',
            '@theme': './src/theme',
            '@components': './src/components',
          },
          extensions: ['.ts', '.tsx', '.js', '.jsx', '.json'],
        },
      ],
      'react-native-reanimated/plugin',
    ],
  };
};
