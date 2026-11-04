module.exports = {
  dependency: {
    platforms: {
      ios: null,
      android: {
        sourceDir: './android',
        packageImportPath: 'import tallpine.mobile.selection.ResponseSelectionPackage;',
        packageInstance: 'new ResponseSelectionPackage()',
      },
    },
  },
};
