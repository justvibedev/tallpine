module.exports = {
  dependency: {
    platforms: {
      ios: {
        podspecPath: `${__dirname}/TallpineImagePaste.podspec`,
      },
      android: {
        sourceDir: './android',
        packageImportPath: 'import tallpine.mobile.imagepaste.ImagePastePackage;',
        packageInstance: 'new ImagePastePackage()',
      },
    },
  },
};
