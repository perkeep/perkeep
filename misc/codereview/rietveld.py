# Copyright (c) 2011 The Chromium Authors. All rights reserved.
# Use of this source code is governed by a BSD-style license that can be
# found in the LICENSE file.
"""Defines class Rietveld to easily access a rietveld instance.

Security implications:

The following hypothesis are made:
- Rietveld enforces:
  - Nobody else than issue owner can upload a patch set
  - Verifies the issue owner credentials when creating new issues
  - A issue owner can't change once the issue is created
  - A patch set cannot be modified
"""

import logging
import os
import sys
import time
import urllib2

try:
  import simplejson as json  # pylint: disable=F0401
except ImportError:
  try:
    import json  # pylint: disable=F0401
  except ImportError:
    # Import the one included in depot_tools.
    sys.path.append(os.path.join(os.path.dirname(__file__), 'third_party'))
    import simplejson as json  # pylint: disable=F0401

from third_party import upload
import patch

# Hack out upload logging.info()
upload.logging = logging.getLogger('upload')
# Mac pylint choke on this line.
upload.logging.setLevel(logging.WARNING)  # pylint: disable=E1103


class Rietveld(object):
  """Accesses rietveld."""
  def __init__(self, url, email, password):
    self.issue = None
    self.url = url
    if email and password:
      get_creds = lambda: (email, password)
      self.rpc_server = upload.HttpRpcServer(
            self.url,
            get_creds)
    else:
      self.rpc_server = upload.GetRpcServer(url, email)
    self._xsrf_token = None
    self._xsrf_token_time = None

  def xsrf_token(self):
    if (not self._xsrf_token_time or
        (time.time() - self._xsrf_token_time) > 30*60):
      self._xsrf_token_time = time.time()
      self._xsrf_token = self.get(
          '/xsrf_token',
          extra_headers={'X-Requesting-XSRF-Token': '1'})
    return self._xsrf_token

  def get_pending_issues(self):
    """Returns an array of dict of all the pending issues on the server."""
    return json.loads(self.get(
        '/search?format=json&commit=True&closed=False&keys_only=True')
        )['results']

  def close_issue(self, issue):
    """Closes the Rietveld issue for this changelist."""
    logging.info('closing issue %s' % issue)
    self.post("/%d/close" % issue, [('xsrf_token', self.xsrf_token())])

  def get_description(self, issue):
    """Returns the issue's description."""
    return self.get('/%d/description' % issue)

  def get_issue_properties(self, issue, messages):
    """Returns all the issue's metadata as a dictionary."""
    url = '/api/%s' % issue
    if messages:
      url += '?messages=true'
    return json.loads(self.get(url))

  def get_patchset_properties(self, issue, patchset):
    """Returns the patchset properties."""
    url = '/api/%s/%s' % (issue, patchset)
    return json.loads(self.get(url))

  def get_file_content(self, issue, patchset, item):
    """Returns the content of a new file.

    Throws HTTP 302 exception if the file doesn't exist or is not a binary file.
    """
    # content = 0 is the old file, 1 is the new file.
    content = 1
    url = '/%s/image/%s/%s/%s' % (issue, patchset, item, content)
    return self.get(url)

  def get_file_diff(self, issue, patchset, item):
    """Returns the diff of the file.

    Returns a useless diff for binary files.
    """
    url = '/download/issue%s_%s_%s.diff' % (issue, patchset, item)
    return self.get(url)

  def get_patch(self, issue, patchset):
    """Returns a PatchSet object containing the details to apply this patch."""
    props = self.get_patchset_properties(issue, patchset) or {}
    out = []
    for filename, state in props.get('files', {}).iteritems():
      status = state.get('status')
      if status is None:
        raise patch.UnsupportedPatchFormat(
            filename, 'File\'s status is None, patchset upload is incomplete')

      # TODO(maruel): That's bad, it confuses property change.
      status = status.strip()

      if status == 'D':
        # Ignore the diff.
        out.append(patch.FilePatchDelete(filename, state['is_binary']))
      elif status in ('A', 'M'):
        # TODO(maruel): Rietveld support is still weird, add this line once it's
        # safe to use.
        # props = state.get('property_changes', '').splitlines() or []
        props = []
        if state['is_binary']:
          out.append(patch.FilePatchBinary(
              filename,
              self.get_file_content(issue, patchset, state['id']),
              props))
        else:
          if state['num_chunks']:
            diff = self.get_file_diff(issue, patchset, state['id'])
          else:
            diff = None
          out.append(patch.FilePatchDiff(filename, diff, props))
      else:
        # Line too long (N/80)
        # pylint: disable=C0301
        # TODO: Add support for MM, A+, etc. Rietveld removes the svn properties
        # from the diff.
        # Example of mergeinfo across branches:
        # http://codereview.chromium.org/202046/diff/1/third_party/libxml/xmlcatalog_dummy.cc
        # svn:eol-style property that is lost in the diff
        # http://codereview.chromium.org/202046/diff/1/third_party/libxml/xmllint_dummy.cc
        # Change with no diff, only svn property change:
        # http://codereview.chromium.org/6462019/
        raise patch.UnsupportedPatchFormat(filename, status)
    return patch.PatchSet(out)

  def update_description(self, issue, description):
    """Sets the description for an issue on Rietveld."""
    logging.info('new description for issue %s' % issue)
    self.post('/%s/description' % issue, [
        ('description', description),
        ('xsrf_token', self.xsrf_token())])

  def add_comment(self, issue, message):
    logging.info('issue %s; comment: %s' % (issue, message))
    return self.post('/%s/publish' % issue, [
        ('xsrf_token', self.xsrf_token()),
        ('message', message),
        ('message_only', 'True'),
        ('send_mail', 'True'),
        ('no_redirect', 'True')])

  def set_flag(self, issue, patchset, flag, value):
    return self.post('/%s/edit_flags' % issue, [
        ('last_patchset', str(patchset)),
        ('xsrf_token', self.xsrf_token()),
        (flag, value)])

  def get(self, request_path, **kwargs):
    kwargs.setdefault('payload', None)
    return self._send(request_path, **kwargs)

  def post(self, request_path, data, **kwargs):
    ctype, body = upload.EncodeMultipartFormData(data, [])
    return self._send(request_path, payload=body, content_type=ctype, **kwargs)

  def _send(self, request_path, **kwargs):
    """Sends a POST/GET to Rietveld.  Returns the response body."""
    maxtries = 5
    for retry in xrange(maxtries):
      try:
        result = self.rpc_server.Send(request_path, **kwargs)
        # Sometimes GAE returns a HTTP 200 but with HTTP 500 as the content. How
        # nice.
        return result
      except urllib2.HTTPError, e:
        if retry >= (maxtries - 1):
          raise
        if e.code not in (500, 502, 503):
          raise
      except urllib2.URLError, e:
        if retry >= (maxtries - 1):
          raise
        if not 'Name or service not known' in e.reason:
          # Usually internal GAE flakiness.
          raise
      # If reaching this line, loop again. Uses a small backoff.
      time.sleep(1+maxtries*2)

  # DEPRECATED.
  Send = get
