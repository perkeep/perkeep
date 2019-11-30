const webpack = require("webpack");

module.exports = {
	mode: 'production',
	entry: "./entry.point",
	output: {
		path: __dirname,
		filename: "prod.inc.js",
		libraryTarget: "this",
	},
	optimization: {
    minimize: true
  }
};
