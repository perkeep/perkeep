package org.camlistore.sigserver;

import java.io.IOException;
import javax.servlet.http.*;

public class SignServlet extends HttpServlet {
  public void doGet(HttpServletRequest req, HttpServletResponse resp)
      throws IOException {
    resp.setContentType("text/plain");
    resp.getWriter().println("Hello, world");
  }
}
