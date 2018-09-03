package aws

import (
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/sagemaker"
	"github.com/hashicorp/terraform/helper/resource"
	"github.com/hashicorp/terraform/helper/schema"
	"log"
	"time"
)

func resourceAwsSagemakerModel() *schema.Resource {
	return &schema.Resource{
		Create: resourceAwsSagemakerModelCreate,
		Read:   resourceAwsSagemakerModelRead,
		Update: resourceAwsSagemakerModelUpdate,
		Delete: resourceAwsSagemakerModelDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},

		Schema: map[string]*schema.Schema{
			"arn": {
				Type:     schema.TypeString,
				Computed: true,
			},

			"name": {
				Type:         schema.TypeString,
				Optional:     true,
				Computed:     true,
				ForceNew:     true,
				ValidateFunc: validateSagemakerName,
			},

			"primary_container": {
				Type:     schema.TypeList,
				Required: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"container_hostname": {
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
						},

						"image": {
							Type:     schema.TypeString,
							Required: true,
							ForceNew: true,
						},

						"model_data_url": {
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
						},

						"environment": {
							Type:     schema.TypeMap,
							Optional: true,
							ForceNew: true,
						},
					},
				},
			},

			"execution_role_arn": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},

			"creation_time": {
				Type:     schema.TypeString,
				Computed: true,
			},

			"tags": tagsSchema(),
		},
	}
}

func resourceAwsSagemakerModelCreate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).sagemakerconn

	var name string
	if v, ok := d.GetOk("name"); ok {
		name = v.(string)
	} else {
		name = resource.UniqueId()
	}

	createOpts := &sagemaker.CreateModelInput{
		ModelName: aws.String(name),
	}

	pContainer := d.Get("primary_container").([]interface{})
	if len(pContainer) > 1 {
		return fmt.Errorf("Only a single primary_container block is expected")
	} else if len(pContainer) == 1 {
		m := pContainer[0].(map[string]interface{})
		createOpts.PrimaryContainer = expandPrimaryContainers(m)
	}

	if v, ok := d.GetOk("execution_role_arn"); ok {
		createOpts.ExecutionRoleArn = aws.String(v.(string))
	}

	if v, ok := d.GetOk("tags"); ok {
		createOpts.Tags = tagsFromMapSagemaker(v.(map[string]interface{}))
	}

	log.Printf("[DEBUG] Sagemaker model create config: %#v", *createOpts)
	modelResponse, err := retryOnAwsCode("ValidationException", func() (interface{}, error) {
		return conn.CreateModel(createOpts)
	})
	model := modelResponse.(*sagemaker.CreateModelOutput)

	if err != nil {
		return fmt.Errorf("Error creating Sagemaker model: %s", err)
	}

	d.SetId(name)
	if err := d.Set("arn", model.ModelArn); err != nil {
		return err
	}

	return resourceAwsSagemakerModelUpdate(d, meta)
}

func resourceAwsSagemakerModelRead(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).sagemakerconn

	request := &sagemaker.DescribeModelInput{
		ModelName: aws.String(d.Id()),
	}

	model, err := conn.DescribeModel(request)
	if err != nil {
		if sagemakerErr, ok := err.(awserr.Error); ok && sagemakerErr.Code() == "ValidationException" {
			d.SetId("")
			return nil
		}
		return fmt.Errorf("Error reading Sagemaker model %s: %s", d.Id(), err)
	}

	if err := d.Set("arn", model.ModelArn); err != nil {
		return err
	}
	if err := d.Set("name", model.ModelName); err != nil {
		return err
	}
	if err := d.Set("execution_role_arn", model.ExecutionRoleArn); err != nil {
		return err
	}
	if err := d.Set("primary_container", flattenPrimaryContainer(model.PrimaryContainer)); err != nil {
		return err
	}
	if err := d.Set("creation_time", model.CreationTime.Format(time.RFC3339)); err != nil {
		return err
	}

	tagsOutput, err := conn.ListTags(&sagemaker.ListTagsInput{
		ResourceArn: model.ModelArn,
	})
	d.Set("tags", tagsToMapSagemaker(tagsOutput.Tags))

	return nil
}

func resourceAwsSagemakerModelUpdate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).sagemakerconn

	d.Partial(true)

	if err := setSagemakerTags(conn, d); err != nil {
		return err
	} else {
		d.SetPartial("tags")
	}

	d.Partial(false)

	return resourceAwsSagemakerModelRead(d, meta)
}

func resourceAwsSagemakerModelDelete(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).sagemakerconn

	deleteOpts := &sagemaker.DeleteModelInput{
		ModelName: aws.String(d.Id()),
	}
	log.Printf("[INFO] Deleting Sagemaker model: %s", d.Id())

	return resource.Retry(5*time.Minute, func() *resource.RetryError {
		_, err := conn.DeleteModel(deleteOpts)
		if err == nil {
			return nil
		}

		sagemakerErr, ok := err.(awserr.Error)
		if !ok {
			return resource.NonRetryableError(err)
		}

		if sagemakerErr.Code() == "ResourceNotFound" {
			return resource.RetryableError(err)
		}

		return resource.NonRetryableError(fmt.Errorf("Error deleting Sagemaker model: %s", err))
	})
}

func expandPrimaryContainers(m map[string]interface{}) *sagemaker.ContainerDefinition {
	container := sagemaker.ContainerDefinition{
		Image: aws.String(m["image"].(string)),
	}

	if v, ok := m["container_hostname"]; ok && v.(string) != "" {
		container.ContainerHostname = aws.String(v.(string))
	}
	if v, ok := m["model_data_url"]; ok && v.(string) != "" {
		container.ModelDataUrl = aws.String(v.(string))
	}
	if v, ok := m["environment"]; ok {
		container.Environment = stringMapToPointers(v.(map[string]interface{}))
	}

	return &container
}

func flattenPrimaryContainer(container *sagemaker.ContainerDefinition) []interface{} {
	cfg := make(map[string]interface{}, 0)

	cfg["image"] = *container.Image

	if container.ContainerHostname != nil {
		cfg["container_hostname"] = *container.ContainerHostname
	}
	if container.ModelDataUrl != nil {
		cfg["model_data_url"] = *container.ModelDataUrl
	}
	if container.Environment != nil {
		cfg["environment"] = flattenEnvironment(container.Environment)
	}

	return []interface{}{cfg}
}

func flattenEnvironment(env map[string]*string) map[string]string {
	m := map[string]string{}
	for k, v := range env {
		m[k] = *v
	}
	return m
}
