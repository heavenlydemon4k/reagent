const { getDefaultConfig } = require('expo/metro-config');

const config = getDefaultConfig(__dirname);

// Support for op-sqlite native module
config.resolver.assetExts.push('db', 'sqlite', 'sqlcipher');
config.resolver.sourceExts = ['ts', 'tsx', 'js', 'jsx', 'json', 'cjs'];

// Enable SQLCipher encryption in op-sqlite
config.resolver.extraNodeModules = {
  ...config.resolver.extraNodeModules,
  'op-sqlite': require.resolve('op-sqlite'),
};

// Hermes + Bytecode optimizations
config.transformer.minifierConfig = {
  keep_classnames: true,
  keep_fnames: true,
  mangle: {
    keep_classnames: true,
    keep_fnames: true,
  },
  output: {
    ascii_only: true,
    quote_keys: true,
    wrap_iife: true,
  },
  sourceMap: {
    includeSources: false,
  },
  toplevel: false,
  compress: {
    reduce_funcs: false,
    passes: 2,
    keep_fnames: true,
    keep_infinity: true,
    pure_getters: true,
  },
};

module.exports = config;
