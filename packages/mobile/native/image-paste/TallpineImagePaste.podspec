require 'json'

package = JSON.parse(File.read(File.join(__dir__, 'package.json')))

Pod::Spec.new do |s|
  s.name         = 'TallpineImagePaste'
  s.version      = package['version']
  s.summary      = 'Paste images into the Tallpine mobile chat composer.'
  s.license      = 'UNLICENSED'
  s.authors      = 'Tallpine'
  s.homepage     = 'https://github.com/justvibedev/tallpine'
  s.platforms    = { :ios => '15.1' }
  s.source       = { :path => '.' }
  s.source_files = 'ios/**/*.{h,m}'
  s.frameworks   = 'UniformTypeIdentifiers'
  s.dependency 'React-Core'
end
