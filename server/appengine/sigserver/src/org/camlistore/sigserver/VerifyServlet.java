package org.camlistore.sigserver;

import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.security.SignatureException;
import java.util.Enumeration;
import java.util.logging.Level;
import java.util.logging.Logger;
import javax.servlet.http.*;

import com.google.gson.JsonObject;
import com.google.gson.JsonParseException;
import com.google.gson.JsonPrimitive;
import com.google.gson.JsonParser;
import org.bouncycastle.bcpg.ArmoredInputStream;
import org.bouncycastle.jce.provider.BouncyCastleProvider;
import org.bouncycastle.openpgp.PGPException;
import org.bouncycastle.openpgp.PGPObjectFactory;
import org.bouncycastle.openpgp.PGPPublicKey;
import org.bouncycastle.openpgp.PGPPublicKeyRingCollection;
import org.bouncycastle.openpgp.PGPSignature;
import org.bouncycastle.openpgp.PGPSignatureList;
import org.bouncycastle.openpgp.PGPUtil;

public class VerifyServlet extends HttpServlet {
  private Logger log = Logger.getLogger(VerifyServlet.class.getName());

  public void doPost(HttpServletRequest req, HttpServletResponse resp)
      throws IOException {

    String sjson = req.getParameter("sjson");
    String keyArmored = req.getParameter("keyarmored"); // Non-standard

    resp.setContentType("text/plain");

    // Validate the signed JSON object and extract the signature and signer.
    int sigIndex = sjson.lastIndexOf(",\"camliSig\":\"");
    if (sigIndex == -1) {
      resp.getWriter().println("Missing camli object signature");
      return;
    }

    String sigJson = "{" + sjson.substring(sigIndex + 1);
    JsonObject sigObj;
    try {
      sigObj = (new JsonParser()).parse(sigJson).getAsJsonObject();
    } catch (JsonParseException e) {
      e.printStackTrace();
      resp.getWriter().println("Invalid JSON signature object: " + e);
      return;
    } catch (IllegalStateException e) {
      e.printStackTrace();
      resp.getWriter().println("Invalid JSON signature object: " + e);
      return;
    }
    if (sigObj.entrySet().size() > 1) {
      resp.getWriter().println(
          "Signature object contains more than 'camliSig':\n" + sigJson);
      return;
    }
    JsonPrimitive sigPrimative = sigObj.getAsJsonPrimitive("camliSig");
    if (sigPrimative == null) {
      resp.getWriter().println("'camliSig' missing from top-level");
      return;
    }
    String camliSig;
    try {
      camliSig = sigPrimative.getAsString();
    } catch (ClassCastException e) {
      e.printStackTrace();
      resp.getWriter().println("Invalid 'camliSig' value: " + e);
      return;
    }

    String camliJson = sjson.substring(0, sigIndex) + "}";
    JsonObject camliObj;
    try {
      camliObj = (new JsonParser()).parse(sjson).getAsJsonObject();
    } catch (JsonParseException e) {
      e.printStackTrace();
      resp.getWriter().println("Invalid JSON object: " + e);
      return;
    } catch (IllegalStateException e) {
      e.printStackTrace();
      resp.getWriter().println("Invalid JSON object: " + e);
      return;
    }
    JsonPrimitive signerPrimative = camliObj.getAsJsonPrimitive("camliSigner");
    if (signerPrimative == null) {
      resp.getWriter().println("'camliSigner' missing from top-level");
      return;
    }
    String camliSigner;
    try {
      camliSigner = signerPrimative.getAsString();
    } catch (ClassCastException e) {
      resp.getWriter().println("Invalid 'camliSigner' value: " + e);
      return;
    }

    log.info("camliSig='" + camliSig + "', " +
             "camliSigner='" + camliSigner + "', " +
             "keyArmored='" + keyArmored + "'");

    // Most of this code originally from the Bouncy Castle PGP example app.
    try {
      PGPObjectFactory pgpFactory = new PGPObjectFactory(
          new ArmoredInputStream(
              new ByteArrayInputStream(camliSig.getBytes("UTF-8")),
              false));
      PGPSignatureList signatureList = (PGPSignatureList) pgpFactory.nextObject();
      PGPSignature sig = signatureList.get(0);
      log.info("Signature found: " + sig);

      PGPPublicKeyRingCollection pubKeyRing =
          new PGPPublicKeyRingCollection(
              new ArmoredInputStream(
                  new ByteArrayInputStream(keyArmored.getBytes("UTF-8"))));
      PGPPublicKey key = pubKeyRing.getPublicKey(sig.getKeyID());

      // NOTE(bslatkin): Can't use BouncyCastle's security provider for crypto
      // operations because of App Engine bug. Doh.
      // http://code.google.com/p/googleappengine/issues/detail?id=1612
      sig.initVerify(key, new BouncyCastleProvider());
      sig.update(camliJson.getBytes("UTF-8"));
      resp.getWriter().println(sig.verify() ? "YES" : "NO");
    } catch (IOException e) {
      log.log(Level.SEVERE, "Input problem", e);
      resp.getWriter().println("Input problem: " + e);
    } catch (PGPException e) {
      log.log(Level.SEVERE, "PGP problem", e);
      resp.getWriter().println("PGP problem: " + e);
    } catch (SignatureException e) {
      log.log(Level.SEVERE, "Signature problem", e);
      resp.getWriter().println("Signature problem: " + e);
    }
  }
}
