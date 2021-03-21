# Storage

## S3

[Amazon S3](https://aws.amazon.com/s3/) is a high-durability key-value store.

To use S3 with Perkeep, you need to:

* Sign up for an Amazon Web Services account.
* Sign into the AWS console, navigate to 'S3'.
* Create an S3 bucket for your Perkeep backups (Perkeep will not work if you put other files in this bucket).
* Configure your Perkeep server to sync blobs to the S3 bucket.

It is advisable to use a dedicated key/secret for Perkeep:

* Sign into the AWS console, navigate to 'Identity & Access Management' and click 'Users'.
* Click 'Create new users' and create a user for Perkeep (keep 'Generate an access key' checked).
* Click 'Show User Security Credentials' to get the users Access key and Secret key - these will be required to configure Perkeep and can't be obtained after you leave this screen.
* Go back to the user list and select the new user, and on the 'permissions' tab add the following 'inline policy' (replacing YOUR_BUCKET_NAME with the name of the bucket you created):

```
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Resource": [
                "arn:aws:s3:::YOUR_BUCKET_NAME",
                "arn:aws:s3:::YOUR_BUCKET_NAME/*"
            ],
            "Sid": "Stmt1464826210000",
            "Effect": "Allow",
            "Action": [
                "s3:DeleteObject",
                "s3:GetBucketLocation",
                "s3:GetObject",
                "s3:ListBucket",
                "s3:PutObject"
            ]
        }
    ]
}
```

Finally, add the s3 config line to your Perkeep `server-config.json`:
```
"s3": "ACCESS_KEY:SECRET_KEY:YOUR_BUCKET_NAME"
```

## B2

[Backblaze B2](https://www.backblaze.com/b2/cloud-storage.html) is simple, reliable, affordable object storage.

To use b2 with Perkep, you need to:

* Sign up for a Backblaze account.
* Sign in to the Backblaze console, navigate to 'Buckets'.
* Create a bucket for your Perkeep backups (Perkeep will not work if you put other files on this bucket).
* Configure your perkeep server to sync blobs to the Backblaze bucket.

It is advisable to create a dedicated Application Key for Perkeep:

* Sign in to the Backblaze console, navigate to 'App Keys'.
* Click 'Add a New Application Key'.
* Give it a name, for example 'perkeep'.
* Allow access to the bucket that you just created.
* Choose 'Read and Write'.
* Click 'Create New Key'.

Finally, add the b2 config line to your perkeep `server-config.json`:
```
"b2": "keyID:applicationKey:bucket"
```
